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
  "regexp"
  "strings"

  "control/internal/proto"
)

// vector tails the eve.json Suricata writes and ships it to clickhouse. Unlike
// proxy and dpipe it is not in the data path: if it stops, VMs keep running and
// policy keeps being enforced, and the only thing lost is the record of what
// happened. So a failure to install it is logged and the daemon carries on.
//
// vector.yaml arrives from the control server whole, like proxy.yaml and for
// the same kind of reason: the transform in it writes exactly the columns the
// control plane's clickhouse migrations create, so the file and the schema have
// to ship together. Rendered here, adding a column would mean rolling a new
// agent to every host before anything could write to it.
//
// What is left on this side is the part that is genuinely the host's: fetching
// the release, putting the file down, and keeping the unit up.
const (
  vectorService = "vector"

  vectorConfigDir  = "/etc/vector"
  vectorConfigPath = "/etc/vector/vector.yaml"

  // vectorDataDir is where vector checkpoints how far it has read and buffers
  // batches clickhouse has not accepted yet. It refuses to start without it.
  vectorDataDir = "/var/lib/vector"
)

// vectorVersionRe mirrors the control server's validation. Checked again here
// because this is where the value becomes a URL whose contents are installed
// and executed as root, and an agent should not depend on the server having
// been careful.
var vectorVersionRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// vectorUnitTemplate is separate from managedUnitTemplate: vector takes
// --config rather than -config, needs no runtime directory, and must not be
// tied to dpipe's.
//
// It runs as root because eve.json is written by the Suricata container as root
// and is not group-readable.
const vectorUnitTemplate = `[Unit]
Description=vector (managed by dagent)
After=network-online.target
Wants=network-online.target
After=dagent.service

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

// vectorVersionMarker records the release the installed binary came from, so a
// version that has not moved does not re-download on every push. In the data
// directory for the same reason the other services' markers are: clearing
// dagent's state forces a clean re-install.
func vectorVersionMarker(data string) string {
  return filepath.Join(data, "services", vectorService+".version")
}

// vectorDownloadURL is the github release artifact. musl rather than gnu: the
// static build runs on any host in the fleet regardless of its libc, which
// matters because these are not all the same distribution.
func vectorDownloadURL(version string) string {
  return fmt.Sprintf(
    "https://github.com/vectordotdev/vector/releases/download/v%s/vector-%s-x86_64-unknown-linux-musl.tar.gz",
    version, version)
}

// applyVectorConfig installs the release the control server named, writes the
// config it sent, and makes sure the unit is up. It reports whether anything
// changed so the caller can say so, and restarts vector only when something did
// -- the server pushes one of these on every connect, and restarting each time
// would drop the read checkpoint's benefit for no reason.
func applyVectorConfig(ctx context.Context, data string, want proto.VectorConfig) (bool, error) {
  // Read here rather than passed in: `dagent connect` does not otherwise hold
  // the config, and the answer only matters when a push arrives.
  dcfg, err := loadConfig("")
  if err != nil {
    return false, fmt.Errorf("could not read the dagent config: %w", err)
  }
  if !dcfg.Vector.Enable {
    return false, errors.New("vector is not enabled on this host, so the config was not installed")
  }
  if want.Config == "" {
    return false, errors.New("no clickhouse url is set in the control server settings, so vector was not installed")
  }
  if !vectorVersionRe.MatchString(want.Version) {
    return false, fmt.Errorf("%q is not a vector release number", want.Version)
  }

  config := want.Config
  if !strings.HasSuffix(config, "\n") {
    config += "\n"
  }

  // Before the unit: vector exits at startup if its data directory is missing,
  // which would look like a crash loop rather than a missing directory.
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

  // 0600: this file holds the clickhouse password, and vector is started by
  // systemd as root. The error below deliberately does not carry the content,
  // and neither does any log line on this path.
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

  // enable --now is idempotent, and it is also the repair for a unit an
  // operator stopped or disabled by hand.
  if err := systemctl(ctx, "enable", "--now", vectorService+".service"); err != nil {
    return false, err
  }
  changed := binaryChanged || unitChanged || configChanged
  if changed {
    // restart, not reload: vector re-reads its config on SIGHUP only if it was
    // started with --watch-config, and a new binary needs an exec either way.
    if err := systemctl(ctx, "restart", vectorService+".service"); err != nil {
      return true, fmt.Errorf("installed the vector config but could not restart it: %w", err)
    }
  }
  return changed, nil
}

// ensureVectorBinary downloads and installs the release if the version moved or
// the binary is missing. Reports whether it installed anything.
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

// downloadVector fetches the release tarball and installs the one file inside
// it that matters. Streamed through the gzip and tar readers rather than
// staged whole: the archive is an order of magnitude larger than the binary,
// and there is no reason for either to touch the disk twice.
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
    // A version that does not exist arrives here as a 404, which is the most
    // likely way this ever fails: someone typed a release that was never cut.
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
    // The archive lays out as vector-<triple>/bin/vector, but the prefix has
    // changed shape across releases, so match the tail rather than the whole
    // path. Type checked so a symlink or a directory named this cannot match.
    if hdr.Typeflag != tar.TypeReg || path.Base(hdr.Name) != "vector" ||
      path.Base(path.Dir(hdr.Name)) != "bin" {
      continue
    }
    return installFromReader(tr, dst)
  }
}

// installFromReader writes r to a temporary file beside dst and renames over
// it. Same dance as downloadBinary, for the same two reasons: overwriting a
// running binary in place fails with ETXTBSY, and a half-written file systemd
// then execs is worse than no file at all.
func installFromReader(r io.Reader, dst string) error {
  tmp, err := os.CreateTemp(filepath.Dir(dst), ".dagent-vector-*")
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

// ensureVectorRunning is the startup half: it re-enables and starts a vector
// that is already installed, so an operator's `systemctl stop` does not outlive
// a dagent restart.
//
// It deliberately installs nothing. The config and the version come from the
// control server, and a host that has never heard from one has nothing to write
// -- it gets vector on its first connect instead.
func ensureVectorRunning(ctx context.Context, cfg VectorService) {
  if !cfg.Enable {
    return
  }
  if _, err := os.Stat(vectorConfigPath); err != nil {
    log.Print("vector is enabled but not configured yet; the control server sends its config on connect")
    return
  }
  if _, err := os.Stat(vectorBinary()); err != nil {
    log.Print("vector is enabled and configured but the binary is missing; it is reinstalled on the next connect")
    return
  }
  if err := systemctl(ctx, "enable", "--now", vectorService+".service"); err != nil {
    log.Printf("WARNING: %s: %v", vectorService, err)
    return
  }
  log.Printf("%s is installed and running as %s.service", vectorService, vectorService)
}

// checkVector is the doctor's read of the shipper. A stopped vector is a
// warning rather than a failure: nothing about running VMs depends on it, and
// the only casualty is the record of what their traffic did.
func checkVector() (result, string) {
  cfg, err := loadConfig("")
  if err != nil {
    return warn, "could not read the dagent config: " + err.Error()
  }
  if !cfg.Vector.Enable {
    return pass, "vector is off; suricata events are not being shipped"
  }
  if _, err := os.Stat(vectorConfigPath); err != nil {
    return warn, "vector is on but has no config yet; the control server sends one on connect"
  }
  out, _ := exec.Command("systemctl", "is-active", vectorService+".service").Output()
  if strings.TrimSpace(string(out)) != "active" {
    return warn, "vector is on but the unit is not active; suricata events are not reaching clickhouse"
  }
  return pass, "vector is running"
}
