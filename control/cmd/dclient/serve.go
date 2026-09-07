package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/urfave/cli/v3"
)

func serveCommand() *cli.Command {
	return &cli.Command{
		Name:  "serve",
		Usage: "run the dclient daemon (network policy, dhcp, metadata, cli socket, control link)",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "config",
				Usage:   "path to config.yaml (default " + configPath + ")",
				Sources: cli.EnvVars("DCLIENT_CONFIG"),
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := loadConfig(cmd.String("config"))
			if err != nil {
				return err
			}
			return runServe(ctx, cfg)
		},
	}
}

func checkFeatures(f Features) error {
	if f.Nftables {
		return nil
	}
	for _, bad := range []struct {
		on   bool
		name string
	}{
		{f.Suricata, "features.suricata"},
		{f.DockerCompat, "features.docker_compat"},
	} {
		if bad.on {
			return fmt.Errorf("%s needs features.nftables, which is off", bad.name)
		}
	}
	return nil
}

func runServe(ctx context.Context, cfg Config) error {
	if os.Geteuid() != 0 {
		return errors.New("serve needs root: it writes nftables rules, network interfaces and sysctls")
	}

	f := cfg.Features
	if err := checkFeatures(f); err != nil {
		return err
	}

	nc, err := cfg.netConfig()
	if err != nil {
		return err
	}
	if err := saveNetConfig(cfg.DataDir, nc); err != nil {
		return err
	}

	if f.IPForward {
		if err := enableForwarding(); err != nil {
			return fmt.Errorf("could not enable ip forwarding: %w", err)
		}
	}
	if f.KVMAccess {
		if msg, err := ensureKVMAccess(); err != nil {
			log.Printf("could not make /dev/kvm reachable by an unprivileged uid (%v); vms will fall back to software emulation", err)
		} else {
			log.Print(msg)
		}
	}
	if f.Nftables {
		if err := applyBaseRuleset(nc); err != nil {
			return err
		}
		recordRulesetGeneration()
		log.Printf("policy installed: pool %s, gateway %s, uplink %s", nc.Pool, nc.Gateway, nc.Uplink)
	}

	ln, err := listen(cfg.Socket, cfg.Group)
	if err != nil {
		return err
	}
	defer func() {
		_ = ln.Close()
		_ = os.Remove(cfg.Socket)
	}()

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	ensureManagedServicesRunning(ctx)
	ensureVectorRunning(ctx)

	errs := make(chan error, 4)

	api := &apiServer{data: cfg.DataDir, cfg: nc}
	go func() { errs <- api.serve(ctx, ln) }()

	if f.Metadata {
		meta := &metadataServer{data: cfg.DataDir, cfg: nc}
		go func() { errs <- meta.serve(ctx) }()
	}

	if f.DHCP {
		dhcp := &dhcpServer{data: cfg.DataDir, cfg: nc}
		go func() { errs <- dhcp.serve(ctx) }()
	}

	if cfg.ControlURL != "" {
		go func() {
			if err := runConnect(cfg.ControlURL, cfg.EnrollmentKey, cfg.DataDir, cfg.DataDir, cfg.Insecure); err != nil {
				log.Printf("control link stopped: %v", err)
			}
		}()
	}

	if !f.Nftables {
		log.Print("features.nftables is off: no packet policy is being installed or repaired")
	}

	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()
	for {
		if f.Nftables {
			if err := reconcile(cfg.DataDir, nc); err != nil {
				log.Printf("reconcile failed: %v", err)
			}
		}
		select {
		case <-ctx.Done():
			log.Print("shutting down")
			return nil
		case err := <-errs:
			if err != nil {
				return err
			}
		case <-ticker.C:
		}
	}
}
