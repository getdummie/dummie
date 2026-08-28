package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"dproxy/internal/proxy"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cfgPath := flag.String("config", "dproxy.yaml", "path to the configuration file")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("dproxy %s (%s, %s)\n", version, commit, date)
		return
	}

	if err := run(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "dproxy:", err)
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

	log.Info("dproxy starting", "control_socket", cfg.ControlSocket)
	return p.Run(ctx)
}
