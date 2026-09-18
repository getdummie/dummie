package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	_ "intproxy/internal/integration/all"
	"intproxy/internal/intproxy"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cfgPath := flag.String("config", "intproxy.yaml", "path to the configuration file")
	check := flag.Bool("check", false, "validate the configuration and exit")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("intproxy %s (%s, %s)\n", version, commit, date)
		return
	}

	if err := run(*cfgPath, *check); err != nil {
		fmt.Fprintln(os.Stderr, "intproxy:", err)
		os.Exit(1)
	}
}

func run(cfgPath string, check bool) error {
	cfg, err := intproxy.LoadConfig(cfgPath)
	if err != nil {
		return err
	}
	if check {
		fmt.Println("intproxy: the configuration is valid")
		return nil
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

	log.Info("intproxy starting", "listen", cfg.Listen, "mode", cfg.Credential.Mode,
		"serving", strings.Join(s.Hostnames(), ","))
	return s.Run(ctx)
}
