package main

import (
	"context"
	"log"
	"os"

	"github.com/urfave/cli/v3"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("dclient: ")

	app := &cli.Command{
		Name:    "dclient",
		Usage:   "control plane client",
		Version: version + " (" + commit + ", " + date + ")",
		Commands: []*cli.Command{
			installCommand(),
			uninstallCommand(),
			serveCommand(),
			connectCommand(),
			doctorCommand(),
			vmCommand(),
			netdCommand(),
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}
