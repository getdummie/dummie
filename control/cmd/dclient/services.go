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
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// dclient installs and runs two companion binaries, proxy and dpipe, as systemd
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

	// binDir is where the downloaded binaries land, alongside dclient itself.
	binDir = "/usr/local/bin"

	// serviceConfigDir holds the yaml each service is started with. Same
	// directory as dclient's own config, because they are one operator surface.
	serviceConfigDir = "/etc/dclient"

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

// managedUnitTemplate is deliberately as plain as dclient's own unit. These are
// long-running network daemons that read a config file and handle their own
// signals; there is nothing for systemd to arrange beyond restarts.
const managedUnitTemplate = `[Unit]
Description=%s (managed by dclient)
After=network-online.target
Wants=network-online.target
# dclient writes this unit and the config it points at, so it should be up first.
After=dclient.service

[Service]
Type=%s
# /run/dpipe, created by systemd before ExecStart. dclient makes this directory
# too, but only once its own startup gets that far -- and dclient is Type=exec, so
# systemd calls this unit started the moment it execs and will happily launch
# dpipe first. Declaring it here is what removes the race.
#
# Preserved on stop because both units name the same directory: without this,
# stopping proxy would delete the socket a running dpipe is still serving.
RuntimeDirectory=dpipe
RuntimeDirectoryPreserve=yes
ExecStart=%s -config %s
%sRestart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
`

// managedUnit renders one companion's unit file.
//
// proxy is a plain Type=simple daemon: it owns the listening sockets the outside
// world arrives on, and replacing it means dropping them, which is what the
// restart-only-when-the-file-changed rule elsewhere is careful about.
//
// dpipe is Type=notify because it can be replaced without dropping anything. A
// `systemctl reload` signals it, and it starts the replacement itself: the new
// process takes the control and upgrade sockets over the existing handover
// protocol and serves immediately, while the old one finishes the sessions it is
// already carrying. The new process is the service from that moment, and it says
// so with MAINPID -- without which systemd would keep watching the old one and
// call the unit dead as soon as it drained.
//
// ExecReload sends a signal rather than starting the replacement directly, which
// is the obvious spelling and does not work: systemd refuses to hand the main pid
// to the process it spawned for ExecReload ("New main PID N is the control
// process, refusing"), so the handover would succeed and the service manager
// would sit there until the reload timed out. A child of the main process is
// accepted, so dpipe forks its own successor.
//
// NotifyAccess=all rather than main for the same reason: the notification comes
// from a process that is not the main pid yet, and `main` would drop it.
func managedUnit(name string) string {
	unitType, reload := "simple", ""
	if name == dpipeService {
		unitType = "notify"
		// RestartForceExitStatus is the guard against a host that has this unit and
		// an older dpipe. SIGHUP terminates a process that does not handle it, and
		// systemd counts death by SIGHUP as a clean exit -- so Restart=on-failure
		// would not fire and a reload would leave the host with no dpipe at all,
		// reported as a success. This turns that combination into a restart.
		//
		// It never fires against a dpipe that understands the signal, because that
		// one does not die of it.
		reload = "NotifyAccess=all\n" +
			"ExecReload=/bin/kill -HUP $MAINPID\n" +
			"RestartForceExitStatus=SIGHUP\n"
	}
	return fmt.Sprintf(managedUnitTemplate,
		name, unitType, serviceBinary(name), serviceConfigPath(name), reload)
}

// defaultDpipeConfig and defaultProxyConfig are written once, on the first start
// after the service is enabled, so the unit has something to read before the
// control server has said anything. Never rewritten from here: clobbering a
// running service's listeners on every restart would be an outage.
//
// Both are bootstrap only. The control server compiles the real version of each
// and pushes it -- proxy.yaml whenever the set of VMs on this host changes,
// dpipe.yaml on every connect -- and the client replaces the file wholesale when
// it does. What is below is what each service runs on until the first push
// arrives, which is also all a host with no control server ever gets.
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

# The browser terminal. proxy decides who may open one; these are only the
# limits on what dpipe will run once it has been handed a session.
console:
  enabled: true
  idle_timeout: 30m
  max_sessions_per_host: 3
`

const defaultProxyConfig = `control_socket: /run/dpipe/control.sock

# Empty for the same reason the ssh users are: the host table is keyed by VM
# name, which is unique across the whole fleet, so it is not something this
# machine can compile on its own.
http:
  listen: "0.0.0.0:80"
  reuseport: true
  hosts: {}
  default: ""

# Empty, not absent: the control server fills this in, and until it does nobody
# may connect. A missing key would read as "not configured" instead.
ssh:
  listen: "0.0.0.0:22"
  reuseport: true
  users: []

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
func serviceUnitPath(name string) string {
	return filepath.Join("/etc/systemd/system", name+".service")
}

// sourceMarker records the URL the installed binary came from. It lives in the
// data directory rather than next to the binary so that removing dclient's state
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

	changed, err := writeIfDifferent(serviceUnitPath(name), managedUnit(name), 0o644)
	if err != nil {
		return err
	}
	if changed {
		if err := systemctl(ctx, "daemon-reload"); err != nil {
			return err
		}
		// A running process keeps the definition it was started with, and Type and
		// NotifyAccess are decided at start. A dpipe started under the old unit was
		// given no NOTIFY_SOCKET, so its replacement could not tell systemd the main
		// pid had moved -- systemd would watch the draining process instead, and call
		// the unit dead the moment it exited while a live dpipe kept serving.
		//
		// So the move onto the new unit costs one restart, which does drop the
		// sessions this host is carrying. It is the last one: every replacement after
		// this is a handover. try-restart rather than restart so a unit that is
		// deliberately stopped stays stopped -- enable --now below is what starts it.
		if err := systemctl(ctx, "try-restart", name+".service"); err != nil {
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

// downloadBinary fetches src and installs it at dst, through a temporary file in
// the same directory that is then renamed. Two reasons for the dance:
// overwriting a running binary in place fails with ETXTBSY, and a half-written
// file that systemd then tries to execute is worse than no file at all.
//
// src is either the binary itself or a .tar.gz holding it -- which is what a
// github release asset is, and so what an upgrade from a published release
// names. The suffix decides; nothing sniffs the bytes, so a tarball served
// without one is a configuration mistake rather than something to guess at.
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

	if isTarball(src) {
		return installFromTarball(src, resp.Body, dst)
	}
	return installFromReader(resp.Body, dst)
}

// isTarball reports whether the URL names a gzipped tar. The path only: a query
// string or fragment says nothing about the format.
func isTarball(src string) bool {
	p := src
	if u, err := url.Parse(src); err == nil {
		p = u.Path
	}
	p = strings.ToLower(p)
	return strings.HasSuffix(p, ".tar.gz") || strings.HasSuffix(p, ".tgz")
}

// installFromTarball pulls the one member named like dst out of the archive and
// installs that. Streamed rather than staged whole, the way downloadVector does
// it: nothing needs the rest of the archive on disk.
//
// A release archive holds the binary at its root, but the member is matched on
// its base name so a tarball that nests it under a directory also works. Type
// checked, so a symlink or directory of that name cannot match -- an archive
// that points its "dpipe" at /etc/shadow does not get to have it copied.
func installFromTarball(src string, body io.Reader, dst string) error {
	gz, err := gzip.NewReader(body)
	if err != nil {
		return fmt.Errorf("%s is not a gzip archive: %w", src, err)
	}
	defer gz.Close()

	want := filepath.Base(dst)
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%s contains no %s", src, want)
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg || path.Base(hdr.Name) != want {
			continue
		}
		return installFromReader(tr, dst)
	}
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

// reloadService replaces a running service without dropping the connections it
// is carrying. Only dpipe can do this -- its unit carries the ExecReload that
// hands the listeners to a fresh process -- so this is a reload for dpipe and a
// restart for anything else.
//
// The fallback covers a unit with no ExecReload, which is what a host running an
// older dclient has. It should not normally be reached: ensureManagedService
// restarts dpipe when it rewrites the unit, so by the time any config or
// certificate arrives the process is already one that can hand over.
//
// Note that a reload returns as soon as the signal is delivered, not once the
// replacement is serving -- the handover runs on its own after that. A failure to
// start the replacement is logged by the process that was signalled, not
// reported here.
func reloadService(ctx context.Context, name string) error {
	if name == dpipeService {
		err := systemctl(ctx, "reload", name+".service")
		if err == nil {
			return nil
		}
		log.Printf("could not reload %s (%v); restarting it instead", name, err)
	}
	return systemctl(ctx, "restart", name+".service")
}

func systemctl(ctx context.Context, args ...string) error {
	out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// removeManagedServices takes both companions down. Used by uninstall: dclient
// started them, so dclient stops them rather than leaving units behind pointing
// at a config directory that is no longer maintained.
func removeManagedServices(ctx context.Context) {
	for _, name := range []string{proxyService, dpipeService, vectorService} {
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
