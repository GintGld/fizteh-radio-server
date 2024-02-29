package models

import (
	"encoding/json"
	"time"
)

// TODO: split into different files when become too big

// TODO: make own json (un)marshalers

// TODO: add more structs (MediaBasicInfo for storage functions)
// to remove pointers. Add methos to convert/create one from another.

type EditorIn struct {
	Login string `json:"login"`
	Pass  string `json:"pass"`
}

type EditorOut struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

type Editor struct {
	ID       int64  `json:"id"`
	Login    string `json:"login"`
	PassHash []byte `json:"pass"`
}

const (
	ErrEditorID int64 = 0

	RootID    int64 = -1
	RootLogin       = "root"
)

type Media struct {
	ID       *int64         `json:"id"`
	Name     *string        `json:"name"`
	Author   *string        `json:"author"`
	Duration *time.Duration `json:"duration"`
	SourceID *int64         `json:"-"`
	Tags     TagList        `json:"tags"`
}

type MediaFilter struct {
	Name       string   `query:"name"`
	Author     string   `query:"name"`
	Tags       []string `query:"tags"`
	MaxRespLen int      `query:"res_len"`
}

type TagTypes []TagType
type TagList []Tag

type Tag struct {
	ID   int64   `json:"id"`
	Name string  `json:"name"`
	Type TagType `json:"type"`
}

type TagType struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Segment struct {
	ID        *int64         `json:"id"`
	MediaID   *int64         `json:"mediaID"`
	Start     *time.Time     `json:"start"`
	BeginCut  *time.Duration `json:"beginCut"`
	StopCut   *time.Duration `json:"stopCut"`
	Protected bool           `json:"protected"`
}

// specify custom time marshalling since
// time package is not stable.
const TimeFormat = "2006-01-02T15:04:05.999999999-07:00"

func (s Segment) MarshalJSON() ([]byte, error) {
	type segmentJSON Segment

	tmp := struct {
		segmentJSON
		Time string `json:"start"`
	}{
		segmentJSON: segmentJSON(s),
		Time:        s.Start.Format(TimeFormat),
	}

	return json.Marshal(tmp)
}
