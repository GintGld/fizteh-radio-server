package router

import (
	"log/slog"
	"time"

	"github.com/GintGld/fizteh-radio/internal/storage/sqlite"
	"golang.org/x/crypto/bcrypt"

	authSrv "github.com/GintGld/fizteh-radio/internal/service/auth"
	jwtSrv "github.com/GintGld/fizteh-radio/internal/service/jwt"
	rootSrv "github.com/GintGld/fizteh-radio/internal/service/root"

	authCtr "github.com/GintGld/fizteh-radio/internal/controller/auth"
	jwtCtr "github.com/GintGld/fizteh-radio/internal/controller/jwt"
	rootCtr "github.com/GintGld/fizteh-radio/internal/controller/root"

	"github.com/gofiber/fiber/v2"
)

type App struct {
	log     *slog.Logger
	address string
	app     *fiber.App
}

// New returns configured router.App
func New(
	log *slog.Logger,
	storage *sqlite.Storage,
	address string,
	tokenTTL time.Duration,
	secret []byte,
	rootPass []byte,
) *App {
	// Create sevices
	jwt := jwtSrv.New(secret)

	rootPassHash, err := bcrypt.GenerateFromPassword(rootPass, bcrypt.DefaultCost)
	if err != nil {
		panic("invalid root password")
	}
	auth := authSrv.New(
		log,
		storage,
		jwt,
		rootPassHash,
		tokenTTL,
	)

	root := rootSrv.New(
		log,
		storage,
	)

	// Create controller helper
	jwtCtr := jwtCtr.New(secret)

	app := fiber.New()

	// Mount controllers to an app
	app.Mount("/login", authCtr.New(auth))
	app.Mount("/root", rootCtr.New(root, jwtCtr))

	return &App{
		log:     log,
		address: address,
		app:     app,
	}
}

func (a *App) MustRun() {
	if err := a.Run(); err != nil {
		panic(err)
	}
}

func (a *App) Run() error {
	return a.app.Listen(a.address)
}

func (a *App) Stop() {
	a.app.Shutdown()
}
