package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/GintGld/fizteh-radio/internal/lib/logger/sl"
	chans "github.com/GintGld/fizteh-radio/internal/lib/utils/channels"
	"github.com/GintGld/fizteh-radio/internal/models"
	"github.com/GintGld/fizteh-radio/internal/service"
	"github.com/GintGld/fizteh-radio/internal/storage"
)

type Schedule struct {
	log          *slog.Logger
	schStorage   ScheduleStorage
	mediaStorage MediaStorage

	allSegmentsChan       chan<- models.Segment
	protectedSegmentsChan chan<- struct{}
}

type ScheduleStorage interface {
	ScheduleCut(ctx context.Context, start time.Time, stop time.Time) ([]models.Segment, error)
	SaveSegment(ctx context.Context, segment models.Segment) (int64, error)
	Segment(ctx context.Context, period int64) (models.Segment, error)
	DeleteSegment(ctx context.Context, period int64) error
	ProtectSegment(ctx context.Context, id int64) error
	IsSegmentProtected(ctx context.Context, id int64) (bool, error)
	ClearSchedule(ctx context.Context, stamp time.Time) error
}

type MediaStorage interface {
	Media(ctx context.Context, id int64) (models.Media, error)
}

func New(
	log *slog.Logger,
	schStorage ScheduleStorage,
	mediaStorage MediaStorage,
	allSegmentsChan chan<- models.Segment,
	protectedSegmentsChan chan<- struct{},
) *Schedule {
	return &Schedule{
		log:                   log,
		schStorage:            schStorage,
		mediaStorage:          mediaStorage,
		allSegmentsChan:       allSegmentsChan,
		protectedSegmentsChan: protectedSegmentsChan,
	}
}

// ScheduleCut returns segments intersecting given interval
func (s *Schedule) ScheduleCut(ctx context.Context, start time.Time, stop time.Time) ([]models.Segment, error) {
	const op = "Schedule.ScheduleCut"

	log := s.log.With(
		slog.String("op", op),
		slog.String("editorname", models.RootLogin),
	)

	segments, err := s.schStorage.ScheduleCut(ctx, start, stop)
	if err != nil {
		log.Error("failed to get schedule cut", sl.Err(err))
		return []models.Segment{}, fmt.Errorf("%s: %w", op, err)
	}

	for i, segment := range segments {
		if isProt, err := s.schStorage.IsSegmentProtected(ctx, *segment.ID); err != nil {
			log.Error("fialed to check segment protection", slog.Int64("id", *segment.ID), sl.Err(err))
			return []models.Segment{}, fmt.Errorf("%s: %w", op, err)
		} else {
			segments[i].Protected = isProt
		}
	}

	return segments, nil
}

// NewSegment registers new segment in schedule
// if media for segment does not exists returns error.
func (s *Schedule) NewSegment(ctx context.Context, segment models.Segment) (int64, error) {
	const op = "Schedule.NewSegment"

	log := s.log.With(
		slog.String("op", op),
		slog.String("editorname", models.RootLogin),
	)

	media, err := s.mediaStorage.Media(ctx, *segment.MediaID)
	if err != nil {
		if errors.Is(err, storage.ErrMediaNotFound) {
			log.Warn("media not found", slog.Int64("id", *segment.MediaID))
			return 0, service.ErrMediaNotFound
		}
		log.Error("failed to get media", slog.Int64("id", *segment.MediaID), sl.Err(err))
		return 0, fmt.Errorf("%s: %w", op, err)
	}

	// Check cut correctness
	if *media.Duration < *segment.StopCut ||
		*media.Duration < *segment.BeginCut ||
		*segment.BeginCut < 0 ||
		*segment.StopCut < 0 {
		log.Warn(
			"invalid cut (out of bounds)",
			slog.Int64("beginCut", segment.BeginCut.Microseconds()),
			slog.Int64("stopCut", segment.StopCut.Microseconds()),
		)
		return 0, service.ErrCutOutOfBounds
	}
	if *segment.BeginCut > *segment.StopCut {
		log.Warn(
			"invalid cut (start after stop)",
			slog.Int64("beginCut", segment.BeginCut.Microseconds()),
			slog.Int64("stopCut", segment.StopCut.Microseconds()),
		)
		return 0, service.ErrBeginAfterStop
	}

	res, err := s.ScheduleCut(ctx, *segment.Start, segment.End())
	if err != nil {
		log.Error("failed to get schedule cut")
		return 0, fmt.Errorf("%s: %w", op, err)
	}

	// If new segment is not protected
	// any intersection causes error.
	if !segment.Protected {
		if len(res) > 0 {
			log.Warn("new not prot. segm has intersection(s)", slog.Any("res", res), slog.Time("start", *segment.Start), slog.Time("end", segment.End()))
			return 0, service.ErrSegmentIntersection
		}
		chans.Send(s.allSegmentsChan, segment)

		id, err := s.schStorage.SaveSegment(ctx, segment)
		if err != nil {
			log.Error("failed to save segment", sl.Err(err))
			return 0, fmt.Errorf("%s: %w", op, err)
		}

		log.Debug("not prot.", slog.Int64("id", id))

		return id, nil
	}

	// If new segment is protected and
	// intersects another protected segment
	// returns error, since can't resolve intersection.
	if j := slices.IndexFunc(res, func(s models.Segment) bool { return s.Protected }); j != -1 {
		log.Warn(
			"detected intersection of protected segments",
			slog.String("found (start-stop)", fmt.Sprintf("%s - %s", res[j].Start.String(), res[j].End().String())),
			slog.String("new (start-stop)", fmt.Sprintf("%s - %s", segment.Start.String(), segment.End().String())),
		)
		// FIXME handle this errors every where.
		return 0, service.ErrSegmentIntersection
	}

	// All intersected segments are
	// not protected. Delete them all.
	for _, segm := range res {
		if segm.End() == *segment.Start || *segm.Start == segm.End() {
			continue
		}
		if err := s.DeleteSegment(ctx, *segm.ID); err != nil {
			if errors.Is(err, service.ErrSegmentNotFound) {
				log.Warn("did not found segment to delete", slog.Int64("segmId", *segm.ID))
				continue
			}
			log.Error("failed to delete segment", slog.Int64("segmId", *segm.ID), sl.Err(err))
			return 0, fmt.Errorf("%s: %w", op, err)
		}
	}

	log.Debug("saving segment")

	// Create segment.
	id, err := s.schStorage.SaveSegment(ctx, segment)
	if err != nil {
		log.Error("failed to save segment", sl.Err(err))
		return 0, fmt.Errorf("%s: %w", op, err)
	}

	log.Debug("saved, protecting", slog.Int64("id", id))

	// Set segment protection.
	if err := s.schStorage.ProtectSegment(ctx, id); err != nil {
		log.Error("failed to set segment protection", sl.Err(err))
		return 0, fmt.Errorf("%s: %w", op, err)
	}

	log.Debug("protected")

	chans.Notify(s.protectedSegmentsChan)
	chans.Send(s.allSegmentsChan, segment)

	return id, nil
}

// Segment returns by its id
func (s *Schedule) Segment(ctx context.Context, id int64) (models.Segment, error) {
	const op = "Schedule.Segment"

	log := s.log.With(
		slog.String("op", op),
		slog.String("editorname", models.RootLogin),
	)

	log.Info("get segment", slog.Int64("id", id))

	segment, err := s.schStorage.Segment(ctx, id)
	if err != nil {
		if errors.Is(err, storage.ErrSegmentNotFound) {
			log.Warn("segment not found", slog.Int64("id", id))
			return models.Segment{}, service.ErrSegmentNotFound
		}
		log.Error("failed to get segment", slog.Int64("id", id))
		return models.Segment{}, fmt.Errorf("%s: %w", op, err)
	}

	log.Info(
		"got segment",
		slog.Int64("id", id),
		slog.Int64("mediaID", *segment.MediaID),
		slog.String("start", segment.Start.Format(models.TimeFormat)),
		slog.Float64("beginCut", segment.BeginCut.Seconds()),
		slog.Float64("stopCut", segment.StopCut.Seconds()),
	)

	isProt, err := s.schStorage.IsSegmentProtected(ctx, id)
	if err != nil {
		log.Error("failed to check segment protection", slog.Int64("id", id), sl.Err(err))
		return models.Segment{}, fmt.Errorf("%s: %w", op, err)
	}

	segment.Protected = isProt

	return segment, nil
}

// DeleteSegment deletes segment by id.
func (s *Schedule) DeleteSegment(ctx context.Context, id int64) error {
	const op = "Schedule.DeleteSegment"

	log := s.log.With(
		slog.String("op", op),
		slog.String("editorname", models.RootLogin),
	)

	isProt, err := s.schStorage.IsSegmentProtected(ctx, id)
	if err != nil {
		if errors.Is(err, storage.ErrSegmentNotFound) {
			log.Warn("segment not found", slog.Int64("id", id))
			return service.ErrSegmentNotFound
		}
		log.Error("failed to check is segment protected")
		return fmt.Errorf("%s: %w", op, err)
	}

	if err := s.schStorage.DeleteSegment(ctx, id); err != nil {
		if errors.Is(err, storage.ErrSegmentNotFound) {
			log.Warn("segment not found", slog.Int64("id", id))
			return service.ErrSegmentNotFound
		}
		log.Error("failed to delete segment", slog.Int64("id", id))
		return fmt.Errorf("%s: %w", op, err)
	}

	if isProt {
		chans.Notify(s.protectedSegmentsChan)
	}

	return nil
}

// ClearSchedule clears schedule from given timestamp.
func (s *Schedule) ClearSchedule(ctx context.Context, from time.Time) error {
	const op = "Schedule.ClearSchedule"

	log := s.log.With(
		slog.String("op", op),
		slog.String("editorname", models.RootLogin),
	)

	log.Info("clearing segments", slog.Time("from", from))

	if err := s.schStorage.ClearSchedule(ctx, from); err != nil {
		log.Error("failed to clear schedule", slog.Time("from", from))
		return fmt.Errorf("%s: %w", op, err)
	}

	log.Info("cleared schedule", slog.String("from", from.Format(models.TimeFormat)))

	return nil
}
