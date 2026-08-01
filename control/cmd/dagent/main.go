// Command dagent is the control-plane agent. It enrolls a machine with the
// control server once, then holds a persistent WebSocket so the server can push
// work down to it.
package main

import (
  "context"
  "log"
  "os"

  "github.com/urfave/cli/v3"
)

// version is reported to the server at enroll and in every hello frame.
const version = "0.1.0"

func main() {
  log.SetFlags(log.LstdFlags | log.Lmsgprefix)
  log.SetPrefix("dagent: ")

  app := &cli.Command{
    Name:    "dagent",
    Usage:   "control plane agent",
    Version: version,
    Commands: []*cli.Command{
      connectCommand(),
      doctorCommand(),
    },
  }

  if err := app.Run(context.Background(), os.Args); err != nil {
    log.Fatal(err)
  }
}
