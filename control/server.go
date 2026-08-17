package main

import (
	"context"
	"log"
	"os"

	"github.com/joho/godotenv"
	"github.com/urfave/cli/v3"
)

func main() {
	// .env is optional; ignore the error when it's absent.
	_ = godotenv.Load()

	app := &cli.Command{
		Name:  "control",
		Usage: "control plane dev orchestrator + API server",
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
