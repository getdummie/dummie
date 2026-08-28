package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"control/internal/proto"
)

func localPool() string {
	cfg, err := loadConfig("")
	if err != nil {
		log.Printf("could not read the dclient config to report the vm pool: %v", err)
		return ""
	}
	return cfg.Network.Pool
}

func applySuricataConfig(ctx context.Context, config string) (bool, error) {
	cfg, err := loadConfig("")
	if err != nil {
		return false, fmt.Errorf("could not read the dclient config: %w", err)
	}
	if !cfg.Features.Suricata {
		return false, errors.New("suricata mode is off on this host, so the config was not installed")
	}

	if !strings.HasSuffix(config, "\n") {
		config += "\n"
	}
	path := suricataConfigPath()
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("could not read %s: %w", path, err)
	}
	if err == nil && string(existing) == config {
		return false, nil
	}
	if err := writeFileAtomic(path, []byte(config), 0o644); err != nil {
		return false, err
	}

	docker, derr := exec.LookPath("docker")
	if derr != nil {
		return true, nil
	}
	if containerState(docker, suricataContainer) != "running" {
		return true, nil
	}
	if out, rerr := exec.CommandContext(ctx, docker, "restart", suricataContainer).CombinedOutput(); rerr != nil {
		return true, fmt.Errorf("wrote %s but could not restart suricata: %v: %s",
			path, rerr, strings.TrimSpace(string(out)))
	}
	return true, nil
}

func applyCoreDNSConfig(ctx context.Context, config string) (bool, error) {
	cfg, err := loadConfig("")
	if err != nil {
		return false, fmt.Errorf("could not read the dclient config: %w", err)
	}
	if !cfg.Features.Suricata {
		return false, errors.New("suricata mode is off on this host, so the corefile was not installed")
	}

	if !strings.HasSuffix(config, "\n") {
		config += "\n"
	}
	existing, err := os.ReadFile(corefilePath)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("could not read %s: %w", corefilePath, err)
	}
	if err == nil && string(existing) == config {
		return false, nil
	}
	if err := os.MkdirAll(corednsConfigDir, 0o755); err != nil {
		return false, fmt.Errorf("could not create %s: %w", corednsConfigDir, err)
	}
	if err := writeFileAtomic(corefilePath, []byte(config), 0o644); err != nil {
		return false, err
	}
	if err := reloadCoreDNS(ctx); err != nil {
		return true, err
	}
	return true, nil
}

func applyDpipeConfig(ctx context.Context, config string, certs *proto.DpipeCerts) (bool, error) {
	if !strings.HasSuffix(config, "\n") {
		config += "\n"
	}
	path := serviceConfigPath(dpipeService)
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("could not read %s: %w", path, err)
	}
	configChanged := err != nil || string(existing) != config

	certsChanged, err := writeDpipeCerts(certs)
	if err != nil {
		return false, err
	}
	if !configChanged && !certsChanged {
		return false, nil
	}

	if configChanged {
		if err := ensureDpipeKeys(); err != nil {
			return false, err
		}
		if err := writeFileAtomic(path, []byte(config), 0o644); err != nil {
			return false, err
		}
	}

	if !serviceInstalled(dpipeService) {
		log.Printf("wrote %s before %s was installed; it starts with that config", path, dpipeService)
		return true, nil
	}

	if err := reloadService(ctx, dpipeService); err != nil {
		return true, fmt.Errorf("wrote %s but could not replace %s: %w", path, dpipeService, err)
	}
	return true, nil
}

func writeDpipeCerts(certs *proto.DpipeCerts) (bool, error) {
	if certs == nil {
		return false, nil
	}

	if err := os.MkdirAll(dpipeCertDir, 0o700); err != nil {
		return false, fmt.Errorf("could not create %s: %w", dpipeCertDir, err)
	}
	if err := os.Chmod(dpipeCertDir, 0o700); err != nil {
		return false, fmt.Errorf("could not tighten %s: %w", dpipeCertDir, err)
	}

	changed := false
	if certs.Cert != "" && certs.Key != "" {
		certChanged, err := writeIfChanged(dpipeCertPath, certs.Cert, 0o644)
		if err != nil {
			return false, err
		}
		keyChanged, err := writeIfChanged(dpipeKeyPath, certs.Key, 0o600)
		if err != nil {
			return false, err
		}
		if certChanged || keyChanged {
			log.Printf("installed a new wildcard certificate in %s", dpipeCertDir)
			changed = true
		}
	}

	namedChanged, err := writeNamedCerts(certs.Named)
	if err != nil {
		return changed, err
	}
	return changed || namedChanged, nil
}

// writeNamedCerts lays out one directory per custom domain and removes the
// directories of domains the control server no longer lists, so a name that
// was dropped stops being served rather than lingering in the SNI table.
func writeNamedCerts(named []proto.DpipeNamedCert) (bool, error) {
	if err := os.MkdirAll(dpipeNamedCertDir, 0o700); err != nil {
		return false, fmt.Errorf("could not create %s: %w", dpipeNamedCertDir, err)
	}

	changed := false
	keep := make(map[string]bool, len(named))
	for _, c := range named {
		if !hostnamePattern.MatchString(c.SNI) || c.Cert == "" || c.Key == "" {
			log.Printf("skipping a certificate for %q: it is not a usable hostname or carries no key", c.SNI)
			continue
		}
		keep[c.SNI] = true

		dir := filepath.Join(dpipeNamedCertDir, c.SNI)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return changed, fmt.Errorf("could not create %s: %w", dir, err)
		}
		certChanged, err := writeIfChanged(filepath.Join(dir, "fullchain.pem"), c.Cert, 0o644)
		if err != nil {
			return changed, err
		}
		keyChanged, err := writeIfChanged(filepath.Join(dir, "privkey.pem"), c.Key, 0o600)
		if err != nil {
			return changed, err
		}
		if certChanged || keyChanged {
			log.Printf("installed a certificate for %s in %s", c.SNI, dir)
			changed = true
		}
	}

	entries, err := os.ReadDir(dpipeNamedCertDir)
	if err != nil {
		return changed, fmt.Errorf("could not read %s: %w", dpipeNamedCertDir, err)
	}
	for _, e := range entries {
		if !e.IsDir() || keep[e.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dpipeNamedCertDir, e.Name())); err != nil {
			return changed, fmt.Errorf("could not remove the certificate for %s: %w", e.Name(), err)
		}
		log.Printf("removed the certificate for %s, which is no longer served here", e.Name())
		changed = true
	}
	return changed, nil
}

var hostnamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

func writeIfChanged(path, content string, mode os.FileMode) (bool, error) {
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("could not read %s: %w", path, err)
	}
	if err == nil && string(existing) == content {
		return false, nil
	}
	if err := writeFileAtomic(path, []byte(content), mode); err != nil {
		return false, err
	}
	return true, nil
}
