package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"intproxy/internal/intproxy"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cfgPath := flag.String("config", "intproxy.yaml", "path to the configuration file")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("intproxy %s (%s, %s)\n", version, commit, date)
		return
	}

	if err := run(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "intproxy:", err)
		os.Exit(1)
	}
}

func run(cfgPath string) error {
	cfg, err := intproxy.LoadConfig(cfgPath)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: intproxy.ParseLogLevel(cfg.LogLevel),
	})).With("pid", os.Getpid())
	slog.SetDefault(log)

	s, err := intproxy.New(cfg, log, version)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	log.Info("intproxy starting", "listen", cfg.Listen, "server_name", s.ServerName())
	return s.Run(ctx)
}
