package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"control/internal/proto"
)

const (
	vectorService = "vector"

	vectorConfigDir  = "/etc/vector"
	vectorConfigPath = "/etc/vector/vector.yaml"

	vectorDataDir = "/var/lib/vector"
)

const vectorUnitTemplate = `[Unit]
Description=vector (managed by dclient)
After=network-online.target
Wants=network-online.target
After=dclient.service

[Service]
Type=simple
ExecStart=%s --config %s
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
`

func vectorBinary() string   { return filepath.Join(binDir, vectorService) }
func vectorUnitPath() string { return serviceUnitPath(vectorService) }

func vectorVersionMarker(data string) string {
	return filepath.Join(data, "services", vectorService+".version")
}

func vectorDownloadURL(version string) string {
	return fmt.Sprintf(
		"https://github.com/vectordotdev/vector/releases/download/v%s/vector-%s-x86_64-unknown-linux-musl.tar.gz",
		version, version)
}

func applyVectorConfig(ctx context.Context, data string, want proto.VectorConfig) (bool, error) {
	if want.Config == "" {
		return false, errors.New("no clickhouse url is set in the control server settings, so vector was not installed")
	}
	if !releaseVersionRe.MatchString(want.Version) {
		return false, fmt.Errorf("%q is not a vector release number", want.Version)
	}

	config := want.Config
	if !strings.HasSuffix(config, "\n") {
		config += "\n"
	}

	for _, dir := range []string{vectorConfigDir, vectorDataDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return false, fmt.Errorf("could not create %s: %w", dir, err)
		}
	}

	binaryChanged, err := ensureVectorBinary(ctx, data, want.Version)
	if err != nil {
		return false, err
	}

	unit := fmt.Sprintf(vectorUnitTemplate, vectorBinary(), vectorConfigPath)
	unitChanged, err := writeIfDifferent(vectorUnitPath(), unit, 0o644)
	if err != nil {
		return false, err
	}
	if unitChanged {
		if err := systemctl(ctx, "daemon-reload"); err != nil {
			return false, err
		}
	}

	existing, err := os.ReadFile(vectorConfigPath)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("could not read %s: %w", vectorConfigPath, err)
	}
	configChanged := err != nil || string(existing) != config
	if configChanged {
		if err := writeFileAtomic(vectorConfigPath, []byte(config), 0o600); err != nil {
			return false, fmt.Errorf("could not write %s", vectorConfigPath)
		}
	}

	if err := systemctl(ctx, "enable", "--now", vectorService+".service"); err != nil {
		return false, err
	}
	changed := binaryChanged || unitChanged || configChanged
	if changed {
		if err := systemctl(ctx, "restart", vectorService+".service"); err != nil {
			return true, fmt.Errorf("installed the vector config but could not restart it: %w", err)
		}
	}
	return changed, nil
}

func ensureVectorBinary(ctx context.Context, data, version string) (bool, error) {
	marker := vectorVersionMarker(data)
	installed, err := os.ReadFile(marker)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if _, err := os.Stat(vectorBinary()); err == nil && strings.TrimSpace(string(installed)) == version {
		return false, nil
	}

	url := vectorDownloadURL(version)
	log.Printf("installing vector %s from %s", version, url)
	if err := downloadVector(ctx, url, vectorBinary()); err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return false, err
	}
	if err := os.WriteFile(marker, []byte(version), 0o600); err != nil {
		return false, err
	}
	return true, nil
}

func downloadVector(ctx context.Context, src, dst string) error {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("could not fetch %s: %w", src, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetching %s: %s", src, resp.Status)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("%s is not a gzip archive: %w", src, err)
	}
	defer gz.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%s contains no bin/vector", src)
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg || path.Base(hdr.Name) != "vector" ||
			path.Base(path.Dir(hdr.Name)) != "bin" {
			continue
		}
		return installFromReader(tr, dst)
	}
}

func installFromReader(r io.Reader, dst string) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".dclient-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	return os.Rename(tmpName, dst)
}

func ensureVectorRunning(ctx context.Context) {
	if _, err := os.Stat(vectorConfigPath); err != nil {
		return
	}
	if _, err := os.Stat(vectorBinary()); err != nil {
		log.Print("vector is configured but the binary is missing; it is reinstalled on the next connect")
		return
	}
	if err := systemctl(ctx, "enable", "--now", vectorService+".service"); err != nil {
		log.Printf("WARNING: %s: %v", vectorService, err)
		return
	}
	log.Printf("%s is installed and running as %s.service", vectorService, vectorService)
}

func checkVector() (result, string) {
	if _, err := os.Stat(vectorConfigPath); err != nil {
		return pass, "vector is not configured on this host; suricata events are not being shipped"
	}
	out, _ := exec.Command("systemctl", "is-active", vectorService+".service").Output()
	if strings.TrimSpace(string(out)) != "active" {
		return warn, "vector is configured but the unit is not active; suricata events are not reaching clickhouse"
	}
	return pass, "vector is running"
}
