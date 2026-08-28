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
		default:
			errs = append(errs, fmt.Errorf("%s: %w", svc.name, err))
		}
	}

	switch staged, err := stageSelfUpgrade(ctx, data, want.Dclient, want.Force); {
	case err == nil:
		upgradeSelf = staged
	case errors.Is(err, errNoRelease):
	default:
		errs = append(errs, fmt.Errorf("dclient: %w", err))
	}

	return upgradeSelf, errors.Join(errs...)
}

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

	if len(installed) == 0 && rel.DownloadURL == "" && rel.Version == version {
		return false, writeSourceMarker(marker, src)
	}

	if !force {
		log.Printf("dclient %s is running but %s is what the control server names; upgrade it from there to move it",
			version, src)
		return false, nil
	}

	if _, err := os.Stat(unitPath); err != nil {
		return false, fmt.Errorf("cannot upgrade: %s is not installed as a service", unitName)
	}

	log.Printf("installing dclient from %s", src)

	stage := filepath.Join(filepath.Dir(installedBinary), ".dclient.upgrade")
	defer func() { _ = os.Remove(stage) }()
	if err := downloadBinary(ctx, src, stage); err != nil {
		return false, err
	}
	if out, err := exec.CommandContext(ctx, stage, "--version").CombinedOutput(); err != nil {
		return false, fmt.Errorf("the downloaded binary does not run: %v: %s",
			err, strings.TrimSpace(string(out)))
	}

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

func restartSelf(ctx context.Context) {
	log.Printf("a new dclient is installed; restarting %s", unitName)
	if err := systemctl(ctx, "restart", "--no-block", unitName); err != nil {
		log.Printf("WARNING: a new dclient is installed but %s could not be restarted (%v); it starts on the next reboot", unitName, err)
	}
}
