// Command proxy is the control plane: it owns the public ingress listeners,
// decides where each connection goes and hands connections to dpipe.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"proxy/internal/proxy"
)

// Set at build time with -X main.version; "dev" in a plain `go build`.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cfgPath := flag.String("config", "proxy.yaml", "path to the configuration file")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("proxy %s (%s, %s)\n", version, commit, date)
		return
	}

	if err := run(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "proxy:", err)
		os.Exit(1)
	}
}

func run(cfgPath string) error {
	cfg, err := proxy.LoadConfig(cfgPath)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: proxy.ParseLogLevel(cfg.LogLevel),
	})).With("pid", os.Getpid())
	slog.SetDefault(log)

	p, err := proxy.New(cfg, log)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	log.Info("proxy starting", "control_socket", cfg.ControlSocket)
	return p.Run(ctx)
}
