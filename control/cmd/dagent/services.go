package main

import (
  "context"
  "errors"
  "fmt"
  "io"
  "log"
  "net/http"
  "net/url"
  "os"
  "os/exec"
  "path/filepath"
  "strings"
  "time"
)

// dagent installs and runs two companion binaries, proxy and dpipe, as systemd
// units of their own rather than as goroutines inside this process. They have
// their own lifecycles -- a proxy that crashes should come back without taking
// the VM control plane with it, and either can be restarted or upgraded on its
// own -- which is exactly what systemd is for.
//
// Everything here is idempotent and runs on every start, for the same reason
// the docker accepts are repaired on every reconcile: a reboot, a manual
// systemctl disable or a half-finished upgrade should converge on the next
// start rather than need an operator.

const (
  proxyService = "proxy"
  dpipeService = "dpipe"

  // binDir is where the downloaded binaries land, alongside dagent itself.
  binDir = "/usr/local/bin"

  // serviceConfigDir holds the yaml each service is started with. Same
  // directory as dagent's own config, because they are one operator surface.
  serviceConfigDir = "/etc/dagent"

  // serviceRuntimeDir is where dpipe puts its control and upgrade sockets, and
  // where proxy goes looking for the control one. /run is a tmpfs, so it is
  // empty after every boot and something has to make this before dpipe starts or
  // it fails to bind.
  serviceRuntimeDir = "/run/dpipe"

  downloadTimeout = 5 * time.Minute
)

// ServiceConfig is one managed companion binary.
type ServiceConfig struct {
  Enable bool `yaml:"enable"`

  // DownloadURL is where the binary comes from. Changing it is what triggers a
  // re-download: the URL is normally the only thing that carries a version, and
  // an operator who points at a new one means to run it.
  DownloadURL string `yaml:"download_url"`
}

// managedUnitTemplate is deliberately as plain as dagent's own unit. These are
// long-running network daemons that read a config file and handle their own
// signals; there is nothing for systemd to arrange beyond restarts.
const managedUnitTemplate = `[Unit]
Description=%s (managed by dagent)
After=network-online.target
Wants=network-online.target
# dagent writes this unit and the config it points at, so it should be up first.
After=dagent.service

[Service]
Type=simple
# /run/dpipe, created by systemd before ExecStart. dagent makes this directory
# too, but only once its own startup gets that far -- and dagent is Type=exec, so
# systemd calls this unit started the moment it execs and will happily launch
# dpipe first. Declaring it here is what removes the race.
#
# Preserved on stop because both units name the same directory: without this,
# stopping proxy would delete the socket a running dpipe is still serving.
RuntimeDirectory=dpipe
RuntimeDirectoryPreserve=yes
ExecStart=%s -config %s
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
`

// defaultDpipeConfig and defaultProxyConfig are written once, on the first start
// after the service is enabled. Never rewritten: after that the file belongs to
// the operator, and clobbering their listeners on a restart would be an outage.
const defaultDpipeConfig = `control_socket: /run/dpipe/control.sock
upgrade_socket: /run/dpipe/upgrade.sock
drain_timeout: 0s
log_level: info

ssh:
  enabled: true
  host_key: /etc/dpipe/keys/dpipe_host_ed25519      # what the user's client pins
  client_key: /etc/dpipe/keys/dpipe_client_ed25519  # what the VM authorizes
  backend_known_hosts: ""
  dial_timeout: 5s
  resolve_timeout: 3s

tls:
  enabled: false
`

const defaultProxyConfig = `control_socket: /run/dpipe/control.sock

http:
  listen: "0.0.0.0:8080"
  reuseport: true
  hosts:
    app.example.com: "10.64.0.2:8000"
  default: ""

dial_timeout: 5s
http_sniff_timeout: 5s
http_sniff_max_bytes: 65536
log_level: info
`

func defaultServiceConfig(name string) string {
  if name == dpipeService {
    return defaultDpipeConfig
  }
  return defaultProxyConfig
}

func serviceBinary(name string) string     { return filepath.Join(binDir, name) }
func serviceConfigPath(name string) string { return filepath.Join(serviceConfigDir, name+".yaml") }
func serviceUnitPath(name string) string   { return filepath.Join("/etc/systemd/system", name+".service") }

// sourceMarker records the URL the installed binary came from. It lives in the
// data directory rather than next to the binary so that removing dagent's state
// forces a clean re-download.
func sourceMarker(data, name string) string {
  return filepath.Join(data, "services", name+".url")
}

// ensureManagedService brings one companion up to the configured state. It
// returns an error rather than logging one, so the caller decides whether a
// service that will not install is fatal.
func ensureManagedService(ctx context.Context, data, name string, cfg ServiceConfig) error {
  if !cfg.Enable {
    return nil
  }
  if err := checkDownloadURL(cfg.DownloadURL); err != nil {
    return err
  }

  // Belt to the unit's braces. RuntimeDirectory only takes effect once the unit
  // is next started, so on a host whose proxy and dpipe are already running this
  // is what puts the directory there now rather than at the next reboot.
  if err := os.MkdirAll(serviceRuntimeDir, 0o755); err != nil {
    return fmt.Errorf("could not create %s: %w", serviceRuntimeDir, err)
  }

  // Before the config is written, and certainly before the unit is started:
  // defaultDpipeConfig turns ssh on and names both key files, so dpipe started
  // without them restart-loops.
  if name == dpipeService {
    if err := ensureDpipeKeys(); err != nil {
      return err
    }
  }

  if err := writeIfAbsent(serviceConfigPath(name), defaultServiceConfig(name), 0o644); err != nil {
    return err
  }

  if err := ensureBinary(ctx, data, name, cfg.DownloadURL); err != nil {
    return err
  }

  unit := fmt.Sprintf(managedUnitTemplate, name, serviceBinary(name), serviceConfigPath(name))
  changed, err := writeIfDifferent(serviceUnitPath(name), unit, 0o644)
  if err != nil {
    return err
  }
  if changed {
    if err := systemctl(ctx, "daemon-reload"); err != nil {
      return err
    }
  }
  // enable --now is idempotent, and it is also the repair: it starts a unit an
  // operator stopped by hand and re-enables one they disabled.
  return systemctl(ctx, "enable", "--now", name+".service")
}

// checkDownloadURL rejects a URL that cannot be fetched at all, and warns about
// one that can be tampered with. Plain http is allowed because the artifact
// server is expected to be on the fleet's own network, but the file it serves is
// executed as root, so whoever can answer for that host owns this one.
func checkDownloadURL(raw string) error {
  if strings.TrimSpace(raw) == "" {
    return errors.New("enabled but download_url is empty")
  }
  u, err := url.Parse(strings.TrimSpace(raw))
  if err != nil {
    return fmt.Errorf("invalid download_url: %w", err)
  }
  switch u.Scheme {
  case "https":
  case "http":
    log.Printf("WARNING: %s is plain http; the binary is unauthenticated and is run as root", u)
  default:
    return fmt.Errorf("download_url must be http or https, got %q", u.Scheme)
  }
  if u.Host == "" {
    return errors.New("download_url is missing a host")
  }
  return nil
}

// ensureBinary downloads the binary if it is missing or if it came from a
// different URL than the one now configured. Anything else -- a rebuild
// published at the same URL, say -- is left alone: re-fetching on every start
// would restart a working service on every boot.
func ensureBinary(ctx context.Context, data, name, src string) error {
  marker := sourceMarker(data, name)
  installed, err := os.ReadFile(marker)
  if err != nil && !os.IsNotExist(err) {
    return err
  }
  if _, err := os.Stat(serviceBinary(name)); err == nil && strings.TrimSpace(string(installed)) == src {
    return nil
  }

  if err := downloadBinary(ctx, src, serviceBinary(name)); err != nil {
    return err
  }
  if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
    return err
  }
  return os.WriteFile(marker, []byte(src), 0o600)
}

// downloadBinary fetches src to dst through a temporary file in the same
// directory, then renames. Two reasons for the dance: overwriting a running
// binary in place fails with ETXTBSY, and a half-written file that systemd then
// tries to execute is worse than no file at all.
//
// Separate from image.go's download, which is a content-addressed cache keyed by
// digest; this one installs to a path the unit file names.
func downloadBinary(ctx context.Context, src, dst string) error {
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

  if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
    return err
  }
  tmp, err := os.CreateTemp(filepath.Dir(dst), ".dagent-*")
  if err != nil {
    return err
  }
  tmpName := tmp.Name()
  defer func() { _ = os.Remove(tmpName) }()

  if _, err := io.Copy(tmp, resp.Body); err != nil {
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

// writeIfAbsent creates a file only if it is not already there, so an operator's
// edits survive every subsequent start.
func writeIfAbsent(path, content string, mode os.FileMode) error {
  if _, err := os.Stat(path); err == nil {
    return nil
  } else if !os.IsNotExist(err) {
    return err
  }
  if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
    return err
  }
  return os.WriteFile(path, []byte(content), mode)
}

// writeIfDifferent reports whether it changed anything, which is what decides
// if a daemon-reload is needed.
func writeIfDifferent(path, content string, mode os.FileMode) (bool, error) {
  existing, err := os.ReadFile(path)
  if err == nil && string(existing) == content {
    return false, nil
  }
  if err != nil && !os.IsNotExist(err) {
    return false, err
  }
  return true, os.WriteFile(path, []byte(content), mode)
}

func systemctl(ctx context.Context, args ...string) error {
  out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
  if err != nil {
    return fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
  }
  return nil
}

// removeManagedServices takes both companions down. Used by uninstall: dagent
// started them, so dagent stops them rather than leaving units behind pointing
// at a config directory that is no longer maintained.
func removeManagedServices(ctx context.Context) {
  for _, name := range []string{proxyService, dpipeService} {
    if _, err := os.Stat(serviceUnitPath(name)); err != nil {
      continue
    }
    if err := systemctl(ctx, "disable", "--now", name+".service"); err != nil {
      fmt.Println(err)
    }
    if err := os.Remove(serviceUnitPath(name)); err != nil && !os.IsNotExist(err) {
      fmt.Println(err)
    }
    fmt.Println("removed " + serviceUnitPath(name))
  }
  _ = systemctl(ctx, "daemon-reload")
}
