// Command dpipe is the data plane: it owns network connections and moves their
// bytes, taking commands from the proxy over a unix socket.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"dpipe/internal/dpipe"
)

func main() {
	cfgPath := flag.String("config", "dpipe.yaml", "path to the configuration file")
	upgrade := flag.Bool("upgrade", false, "take over the listeners of the running instance")
	flag.Parse()

	if err := run(*cfgPath, *upgrade); err != nil {
		fmt.Fprintln(os.Stderr, "dpipe:", err)
		os.Exit(1)
	}
}

func run(cfgPath string, upgrade bool) error {
	cfg, err := dpipe.LoadConfig(cfgPath)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: dpipe.ParseLogLevel(cfg.LogLevel),
	})).With("pid", os.Getpid())
	slog.SetDefault(log)

	srv, err := dpipe.New(cfg, log)
	if err != nil {
		return err
	}

	if upgrade {
		if err := srv.AdoptRunning(); err != nil {
			return err
		}
		log.Info("dpipe started via upgrade")
	} else {
		if err := srv.Bind(); err != nil {
			return err
		}
		srv.Start()
		log.Info("dpipe started")
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)

	select {
	case s := <-sig:
		log.Info("signal received", "signal", s.String())
		srv.Shutdown()
	case <-srv.Exit():
		log.Info("handed over: drained, exiting")
	}
	return nil
}
