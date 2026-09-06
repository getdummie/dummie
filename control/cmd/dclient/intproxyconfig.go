package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"control/internal/proto"
)

func applyIntproxyConfig(ctx context.Context, cfg proto.IntproxyConfig) (bool, error) {
	config := cfg.Config
	if !strings.HasSuffix(config, "\n") {
		config += "\n"
	}

	certsChanged, err := writeIntproxyCerts(cfg.Cert, cfg.Key)
	if err != nil {
		return false, err
	}

	path := serviceConfigPath(intproxyService)
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("could not read %s: %w", path, err)
	}
	configChanged := err != nil || string(existing) != config

	// Only restart on a real change: a restart drops every clone in flight, and
	// this config is otherwise static per host.
	if !configChanged && !certsChanged {
		return false, nil
	}

	if configChanged {
		if err := writeFileAtomic(path, []byte(config), 0o644); err != nil {
			return false, err
		}
	}

	if !serviceInstalled(intproxyService) {
		log.Printf("wrote %s before %s was installed; it starts with that config", path, intproxyService)
		return true, nil
	}

	if err := systemctl(ctx, "restart", intproxyService+".service"); err != nil {
		return true, fmt.Errorf("wrote %s but could not restart %s: %w", path, intproxyService, err)
	}
	return true, nil
}

// writeIntproxyCerts installs the fleet wildcard. It is the same certificate
// dpipe serves; intproxy gets its own copy so the two stay independently
// installable, and both are 0600 root.
func writeIntproxyCerts(cert, key string) (bool, error) {
	if cert == "" || key == "" {
		return false, nil
	}
	if err := os.MkdirAll(intproxyCertDir, 0o700); err != nil {
		return false, fmt.Errorf("could not create %s: %w", intproxyCertDir, err)
	}

	certChanged, err := writeIfChangedFile(intproxyCertFile, cert, 0o644)
	if err != nil {
		return false, err
	}
	keyChanged, err := writeIfChangedFile(intproxyKeyFile, key, 0o600)
	if err != nil {
		return false, err
	}
	if certChanged || keyChanged {
		log.Printf("installed the integration certificate in %s", intproxyCertDir)
	}
	return certChanged || keyChanged, nil
}

func writeIfChangedFile(path, content string, mode os.FileMode) (bool, error) {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("could not read %s: %w", path, err)
	}
	if err == nil && string(existing) == content {
		return false, nil
	}
	if err := writeFileAtomic(path, []byte(content), mode); err != nil {
		return false, fmt.Errorf("could not write %s: %w", path, err)
	}
	return true, nil
}
