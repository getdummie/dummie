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

// `dclient serve` is what the systemd unit runs. It is everything privileged in
// one process: the network policy and its reconciler, DHCP, metadata, the CLI's
// socket, and -- when a control server is configured -- the websocket that
// carries work down from it.
//
// One process rather than several because they share the same state directory
// and the same kernel objects. Two writers to nftables was a real hazard when
// `netd` and an occasional `sudo vm create` both existed.
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

// checkFeatures rejects combinations that would start something with nothing
// behind it. Both of these are enforced by the packet policy: Suricata only ever
// sees traffic the ruleset queues to it, and the DOCKER-USER accepts exist to
// let traffic reach a ruleset that has to be there to accept it. Running either
// without nftables is a config that does nothing, quietly, which is worse than
// refusing to start.
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
	// Persisted so the direct (root, no daemon) path and `netd teardown` agree
	// with the daemon about the pool, gateway and queue count. Inside dclient's own
	// data directory, so it is not one of the things features gate.
	if err := saveNetConfig(cfg.DataDir, nc); err != nil {
		return err
	}

	if f.IPForward {
		if err := enableForwarding(); err != nil {
			return fmt.Errorf("could not enable ip forwarding: %w", err)
		}
	}
	// Repaired on every start, not once at install: /dev is rebuilt at boot, and
	// in a container it is rebuilt whenever the container is. Not fatal -- a host
	// with no usable /dev/kvm still runs guests, just slowly.
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
		log.Printf("policy installed: pool %s, gateway %s, uplink %s", nc.Pool, nc.Gateway, nc.Uplink)
	}

	// Bind the socket before anything long-running, so a permissions problem
	// fails immediately rather than after the network is half set up.
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

	// None of the companions is installed from here any more: which build each one
	// runs comes from the control server, so all a start can do is bring back what
	// a previous push already put down. A host that has never connected gets them
	// on its first connect instead.
	ensureManagedServicesRunning(ctx)
	ensureVectorRunning(ctx)

	errs := make(chan error, 4)

	// The CLI socket is not gated: it is the daemon, not something the daemon
	// does to the host, and without it `serve` is a process with no way in.
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

	// The control link is optional: a host with no control server is still a
	// perfectly good standalone dclient.
	if cfg.ControlURL != "" {
		go func() {
			if err := runConnect(cfg.ControlURL, cfg.EnrollmentKey, cfg.DataDir, cfg.DataDir, cfg.Insecure); err != nil {
				// Not fatal to the daemon -- losing the control plane must not take
				// local VM management down with it.
				log.Printf("control link stopped: %v", err)
			}
		}()
	}

	// Docker compat, Suricata and the resolver are repaired inside reconcile, so
	// they follow nftables: checkFeatures has already rejected a config that asks
	// for any of them without it.
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
