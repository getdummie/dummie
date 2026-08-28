package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"control/internal/proto"
)

// Which build of dclient, dpipe and dproxy this host runs is the control server's
// decision. It used to be the host's, named as a download_url per service in
// /etc/dclient/config.yaml, which meant an upgrade was an ssh loop and nothing in
// the control plane knew what any machine was actually running.
//
// The two companions are the easy half: they are separate units, so installing one
// is a download and a systemctl. dclient is the awkward one -- upgrading it means
// replacing the binary of the running process and then asking systemd to restart
// it, which is why that path stages, verifies and only then swaps.

// applyServicesConfig brings all three binaries up to the builds the control
// server named.
//
// It reports whether this process needs replacing rather than doing it: the
// restart takes the socket down, so the caller answers the job first and pulls the
// floor out afterwards.
//
// Each service is independent. A dpipe that will not install must not stop dproxy
// from being installed, and neither should stop the dclient upgrade -- so the
// errors are collected and reported together rather than returned at the first
// failure.
func applyServicesConfig(ctx context.Context, data string, want proto.ServicesConfig) (bool, error) {
	var (
		upgradeSelf bool
		errs        []error
	)

	for _, svc := range []struct {
		name string
		rel  proto.ServiceRelease
	}{
		{proxyService, want.Proxy},
		{dpipeService, want.Dpipe},
	} {
		switch err := ensureManagedService(ctx, data, svc.name, svc.rel, want.Force); {
		case err == nil:
			log.Printf("%s is installed and running as %s.service", svc.name, svc.name)
		case errors.Is(err, errNoRelease):
			// Nothing named, nothing to do. Silent: on a fleet run by a development
			// build of the control server this is every connect.
		default:
			errs = append(errs, fmt.Errorf("%s: %w", svc.name, err))
		}
	}

	switch staged, err := stageSelfUpgrade(ctx, data, want.Dclient, want.Force); {
	case err == nil:
		upgradeSelf = staged
	case errors.Is(err, errNoRelease):
		// Nothing named, so this process stays as it is.
	default:
		errs = append(errs, fmt.Errorf("dclient: %w", err))
	}

	return upgradeSelf, errors.Join(errs...)
}

// stageSelfUpgrade puts the named build of dclient in place, and reports whether
// it did -- in which case the process is still the old one and has to be
// restarted for the swap to mean anything.
//
// The marker is what decides, not the running version: a release whose tarball
// turns out to contain a different version than it claims would otherwise
// re-install and restart on every single connect. Keyed on the resolved URL, so
// moving the version or pointing at a different build both trigger, and neither
// triggers twice.
//
// force is why a connect does not do this. Replacing dclient restarts the host's
// whole control plane, and the version it is being moved to is often the control
// server's own -- so without the flag, deploying the control server would restart
// every dclient in the fleet.
func stageSelfUpgrade(ctx context.Context, data string, rel proto.ServiceRelease, force bool) (bool, error) {
	src, err := resolveRelease("dclient", rel)
	if err != nil {
		return false, err
	}

	marker := sourceMarker(data, "dclient")
	installed, err := os.ReadFile(marker)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if strings.TrimSpace(string(installed)) == src {
		return false, nil
	}

	// No marker and the running version is already the target: this host was
	// installed by hand at the right version. Record it and stop, rather than
	// downloading what is already running and restarting to prove it.
	if len(installed) == 0 && rel.DownloadURL == "" && rel.Version == version {
		return false, writeSourceMarker(marker, src)
	}

	if !force {
		log.Printf("dclient %s is running but %s is what the control server names; upgrade it from there to move it",
			version, src)
		return false, nil
	}

	// Refused rather than half-done: without the unit there is nothing that would
	// start the new binary, so the swap would leave this process running an old
	// build that no longer exists on disk and no way to get the new one going.
	if _, err := os.Stat(unitPath); err != nil {
		return false, fmt.Errorf("cannot upgrade: %s is not installed as a service", unitName)
	}

	log.Printf("installing dclient from %s", src)

	// Staged next to the target so the swap below is a rename within one
	// filesystem, and verified before it: a binary that does not run would take
	// the host's whole control plane with it, and systemd would restart-loop on it.
	stage := filepath.Join(filepath.Dir(installedBinary), ".dclient.upgrade")
	defer func() { _ = os.Remove(stage) }()
	if err := downloadBinary(ctx, src, stage); err != nil {
		return false, err
	}
	if out, err := exec.CommandContext(ctx, stage, "--version").CombinedOutput(); err != nil {
		return false, fmt.Errorf("the downloaded binary does not run: %v: %s",
			err, strings.TrimSpace(string(out)))
	}

	// The marker first. If the rename succeeds and writing the marker then fails,
	// the next connect would install the same build again -- and every connect
	// after it, since the swap is what makes the versions match. Written first, the
	// worst case is a marker claiming a build that is not there, which the missing
	// binary and the failing unit both make obvious.
	if err := writeSourceMarker(marker, src); err != nil {
		return false, err
	}
	if err := os.Rename(stage, installedBinary); err != nil {
		return false, err
	}
	return true, nil
}

func writeSourceMarker(marker, src string) error {
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte(src), 0o600)
}

// restartSelf hands this process's replacement to systemd.
//
// --no-block because systemd would otherwise be waiting for this unit to stop
// while this unit waits for systemctl to return, and the stop is what kills the
// process holding it. With it, the command returns, and the SIGTERM arrives
// immediately afterwards.
func restartSelf(ctx context.Context) {
	log.Printf("a new dclient is installed; restarting %s", unitName)
	if err := systemctl(ctx, "restart", "--no-block", unitName); err != nil {
		// Worth shouting about: the binary on disk is the new one and this process is
		// the old one, and nothing else will notice until someone restarts the host.
		log.Printf("WARNING: a new dclient is installed but %s could not be restarted (%v); it starts on the next reboot", unitName, err)
	}
}
