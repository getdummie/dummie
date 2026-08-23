// Command dpipe is the data plane: it owns network connections and moves their
// bytes, taking commands from the proxy over a unix socket.
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

// Set at build time with -X main.version; "dev" in a plain `go build`.
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
		// The upgrade path notifies from inside AdoptRunning, where it has to
		// happen before the old process is told it may drain.
		if err := dpipe.NotifyReady(); err != nil {
			log.Warn("could not notify systemd", "err", err)
		}
		log.Info("dpipe started")
	}

	// SIGHUP is the upgrade request. It is handled here, by the running process,
	// rather than by a unit that starts the replacement itself: systemd refuses to
	// hand the main pid to the process it spawned for ExecReload ("New main PID N
	// is the control process, refusing"), so the handover would succeed and the
	// service manager would not follow it. A child of the main process is accepted,
	// which is why this one is started from here.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	for {
		select {
		case s := <-sig:
			if s == syscall.SIGHUP {
				if err := spawnUpgrade(cfgPath, srv, log); err != nil {
					// Not fatal: this process still owns every listener and every
					// session, so a replacement that could not be started leaves a
					// working service rather than none.
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

// spawnUpgrade starts a replacement process that will take this one's listeners.
//
// The child re-execs this same binary from disk, so a reload after the file has
// been replaced is also how a new version is rolled out -- there is no separate
// upgrade path, and no restart in either case.
//
// Nothing is waited for here. The handover runs over the upgrade socket on its
// own goroutine, and this process finds out it is done the same way it always
// does: the registry drains and Exit() closes.
func spawnUpgrade(cfgPath string, srv *dpipe.Server, log *slog.Logger) error {
	// A second replacement while the first is draining would be two processes
	// racing for the same listeners, and the loser exits having taken none.
	if srv.Draining() {
		return errors.New("this process has already handed over and is draining")
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not find this binary: %w", err)
	}

	cmd := exec.Command(exe, "-config", cfgPath, "--upgrade")
	// Inherited deliberately: NOTIFY_SOCKET is in here, and without it the child
	// cannot tell systemd that the main pid moved.
	cmd.Env = os.Environ()
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	// Its own session, so the child does not go down with the process group this
	// one is in. It stays inside the unit's cgroup either way, which is what keeps
	// systemd's accounting and its stop behaviour correct.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return err
	}
	log.Info("started a replacement", "pid", cmd.Process.Pid)
	// Reaped so the child is not left a zombie in the window before this process
	// exits. It normally outlives us, in which case Wait simply never returns and
	// the goroutine goes with the process.
	go func() { _ = cmd.Wait() }()
	return nil
}
