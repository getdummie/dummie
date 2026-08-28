package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"control/internal/proto"
)

func applyProxyConfig(ctx context.Context, cfg proto.ProxyConfig) (bool, error) {
	config := cfg.Config
	if !strings.HasSuffix(config, "\n") {
		config += "\n"
	}

	secretChanged, err := writeProxyCookieSecret(cfg.CookieSecretPath, cfg.CookieSecret)
	if err != nil {
		return false, err
	}

	siteChanged, err := writeProxySitePage(cfg.SitePath, cfg.SiteHTML)
	if err != nil {
		return false, err
	}

	path := serviceConfigPath(proxyService)
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("could not read %s: %w", path, err)
	}
	configChanged := err != nil || string(existing) != config
	if !configChanged && !secretChanged && !siteChanged {
		return false, nil
	}

	if configChanged {
		if err := writeFileAtomic(path, []byte(config), 0o644); err != nil {
			return false, err
		}
	}

	if !serviceInstalled(proxyService) {
		log.Printf("wrote %s before %s was installed; it starts with that config", path, proxyService)
		return true, nil
	}

	if err := systemctl(ctx, "restart", proxyService+".service"); err != nil {
		return true, fmt.Errorf("wrote %s but could not restart %s: %w", path, proxyService, err)
	}
	return true, nil
}

func writeProxyCookieSecret(path, secret string) (bool, error) {
	if secret == "" {
		return false, nil
	}
	if path == "" {
		path = dpipeCookieSecretPath
	}

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("could not read %s: %w", path, err)
	}
	if err == nil && string(existing) == secret {
		return false, nil
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, fmt.Errorf("could not create %s: %w", dir, err)
	}
	if err := writeNewFile(path, []byte(secret), 0o600); err != nil {
		return false, fmt.Errorf("could not write the proxy cookie secret to %s: %w", path, err)
	}
	log.Printf("wrote the proxy cookie secret to %s", path)
	return true, nil
}

func writeProxySitePage(path, html string) (bool, error) {
	if html == "" || path == "" {
		return false, nil
	}

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("could not read %s: %w", path, err)
	}
	if err == nil && string(existing) == html {
		return false, nil
	}
	if err := writeFileAtomic(path, []byte(html), 0o644); err != nil {
		return false, fmt.Errorf("could not write the proxy site page to %s: %w", path, err)
	}
	log.Printf("wrote the proxy site page to %s", path)
	return true, nil
}

func writeFileAtomic(path string, content []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("could not create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("could not stage %s: %w", path, err)
	}
	staged := tmp.Name()
	defer func() { _ = os.Remove(staged) }()

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("could not write %s: %w", path, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(staged, path)
}
