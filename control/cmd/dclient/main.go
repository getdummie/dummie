// Command dclient is the control-plane client. It enrolls a machine with the
// control server once, then holds a persistent WebSocket so the server can push
// work down to it.
package main

import (
	"context"
	"log"
	"os"

	"github.com/urfave/cli/v3"
)

// version is reported to the server at enroll and in every hello frame. Set at
// build time with -X main.version; "dev" in a plain `go build`.
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
