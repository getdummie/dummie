package main

import (
	"context"
	"log"
	"os"

	"github.com/joho/godotenv"
	"github.com/urfave/cli/v3"
)

// Set at build time with -X main.version; "dev" in a plain `go build`.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// The general API info swag reads. It lives on main() because that is the
// declaration `swag init -g server.go` looks at, and because the description is
// about the whole surface rather than about any one route.
//
// @title       dummie control API
// @version     1.0
// @description The self-service API: sign in, manage your profile and your personal access tokens, and create and run VMs.
// @description
// @description Two credentials reach these routes. A browser sends the httpOnly `access_token` cookie. A script sends a personal access token as `Authorization: Bearer dpat_...` — mint one under Settings, or with `POST /me/tokens` from a signed-in session.
// @description
// @description A personal access token does everything you can do with your own VMs. It can never reach `/api/v1/admin/*`, even when it belongs to an admin, and it cannot manage tokens: those routes take a password-backed session, so a leaked token cannot mint its successor.
// @description
// @description Admin and client-enrollment routes are deliberately absent here. The first is browser-only by design; the second is a protocol between the control plane and its hosts rather than an API.
//
// @BasePath                   /api/v1
// @securityDefinitions.apikey BearerAuth
// @in                         header
// @name                       Authorization
func main() {
	// .env is optional; ignore the error when it's absent.
	_ = godotenv.Load()

	app := &cli.Command{
		Name:    "control",
		Usage:   "control plane dev orchestrator + API server",
		Version: version + " (" + commit + ", " + date + ")",
		Commands: []*cli.Command{
			serveCommand(),
			migrateCommand(postgresMigrations),
			migrateCommand(clickhouseMigrations),
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}
