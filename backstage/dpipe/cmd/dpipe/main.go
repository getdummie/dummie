package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"dpipe/internal/dpipe"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cfgPath := flag.String("config", "dpipe.yaml", "path to the configuration file")
	upgrade := flag.Bool("upgrade", false, "take over the listeners of the running instance")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("dpipe %s (%s, %s)\n", version, commit, date)
		return
	}

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
		if err := dpipe.NotifyReady(); err != nil {
			log.Warn("could not notify systemd", "err", err)
		}
		log.Info("dpipe started")
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	for {
		select {
		case s := <-sig:
			if s == syscall.SIGHUP {
				if err := spawnUpgrade(cfgPath, srv, log); err != nil {
					log.Warn("could not start the replacement", "err", err)
				}
				continue
			}
			log.Info("signal received", "signal", s.String())
			srv.Shutdown()
			return nil
		case <-srv.Exit():
			log.Info("handed over: drained, exiting")
			return nil
		}
	}
}

func spawnUpgrade(cfgPath string, srv *dpipe.Server, log *slog.Logger) error {
	if srv.Draining() {
		return errors.New("this process has already handed over and is draining")
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not find this binary: %w", err)
	}

	cmd := exec.Command(exe, "-config", cfgPath, "--upgrade")
	cmd.Env = os.Environ()
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return err
	}
	log.Info("started a replacement", "pid", cmd.Process.Pid)
	go func() { _ = cmd.Wait() }()
	return nil
}
