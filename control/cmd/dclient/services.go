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
	"regexp"
	"runtime"
	"strings"
	"time"

	"control/internal/proto"
)

const (
	proxyService = "dproxy"
	dpipeService = "dpipe"

	serviceConfigDir = "/etc/dclient"

	serviceRuntimeDir = "/run/dpipe"

	downloadTimeout = 5 * time.Minute
)

// binDir is where the managed binaries are installed. It is a var only so a
// test can point it somewhere writable.
var binDir = "/usr/local/bin"

const releaseURLTemplate = "https://github.com/getdummie/dummie/releases/download/v%s/%s_%s_linux_%s.tar.gz"

var releaseVersionRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func releaseURL(name, version string) string {
	return fmt.Sprintf(releaseURLTemplate, version, name, version, runtime.GOARCH)
}

func resolveRelease(name string, rel proto.ServiceRelease) (string, error) {
	if rel.DownloadURL != "" {
		if err := checkDownloadURL(rel.DownloadURL); err != nil {
			return "", err
		}
		return rel.DownloadURL, nil
	}
	if rel.Version == "" {
		return "", errNoRelease
	}
	if !releaseVersionRe.MatchString(rel.Version) {
		return "", fmt.Errorf("%q is not a release number", rel.Version)
	}
	return releaseURL(name, rel.Version), nil
}

var errNoRelease = errors.New("no version or download url was named")

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

func managedUnit(name string) string {
	unitType, reload := "simple", ""
	if name == dpipeService {
		unitType = "notify"
		reload = "NotifyAccess=all\n" +
			"ExecReload=/bin/kill -HUP $MAINPID\n" +
			"RestartForceExitStatus=SIGHUP\n"
	}
	return fmt.Sprintf(managedUnitTemplate,
		name, unitType, serviceBinary(name), serviceConfigPath(name), reload)
}

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

# No ssh block at all, deliberately. dproxy refuses to start on an ssh listener
# with no users -- an ingress that can authenticate nobody is a typo in almost
# every case it appears in a file someone wrote -- and the user list is compiled
# from who owns which VM, so this host cannot write one. Absent is how "not
# configured yet" is spelled: dproxy reads it as no ssh ingress, which is the
# closed reading, and the first push from the control server adds the block.
#
# So the bootstrap dproxy serves http to an empty host table and nothing else.

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

func sourceMarker(data, name string) string {
	return filepath.Join(data, "services", name+".url")
}

func ensureManagedService(ctx context.Context, data, name string, rel proto.ServiceRelease, force bool) error {
	src, err := resolveRelease(name, rel)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(serviceRuntimeDir, 0o755); err != nil {
		return fmt.Errorf("could not create %s: %w", serviceRuntimeDir, err)
	}

	if name == dpipeService {
		if err := ensureDpipeKeys(); err != nil {
			return err
		}
	}

	if err := writeIfAbsent(serviceConfigPath(name), defaultServiceConfig(name), 0o644); err != nil {
		return err
	}

	// Whether it was already up decides if swapping the binary needs a handover:
	// enable --now below starts a stopped unit on the new build anyway.
	wasActive := serviceActive(ctx, name)

	replaced, err := ensureBinary(ctx, data, name, src, force)
	if err != nil {
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
		if err := systemctl(ctx, "try-restart", name+".service"); err != nil {
			return err
		}
	}
	if err := systemctl(ctx, "enable", "--now", name+".service"); err != nil {
		return err
	}

	// A rename over the binary leaves the running process on the old inode, so
	// without this it serves the previous build until something else restarts it.
	if replaced && wasActive && !changed {
		log.Printf("%s was replaced by a new build; moving the running process onto it", name)
		return reloadService(ctx, name)
	}
	return nil
}

func serviceActive(ctx context.Context, name string) bool {
	return systemctl(ctx, "is-active", "--quiet", name+".service") == nil
}

func installedServiceVersion(ctx context.Context, name string) string {
	out, err := exec.CommandContext(ctx, serviceBinary(name), "-version").Output()
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return ""
	}
	return fields[1]
}

func installedServicesState(ctx context.Context) *proto.ServicesState {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	return &proto.ServicesState{
		Dpipe: installedServiceVersion(ctx, dpipeService),
		Proxy: installedServiceVersion(ctx, proxyService),
		Dinit: installedServiceVersion(ctx, guestInitService),
	}
}

func serviceInstalled(name string) bool {
	_, err := os.Stat(serviceUnitPath(name))
	return err == nil
}

func ensureManagedServicesRunning(ctx context.Context) {
	for _, name := range []string{proxyService, dpipeService} {
		if !serviceInstalled(name) {
			continue
		}
		if _, err := os.Stat(serviceBinary(name)); err != nil {
			log.Printf("%s has a unit but no binary; it is reinstalled on the next connect", name)
			continue
		}
		if err := systemctl(ctx, "enable", "--now", name+".service"); err != nil {
			log.Printf("WARNING: %s: %v", name, err)
			continue
		}
		log.Printf("%s is installed and running as %s.service", name, name)
	}
}

func checkDownloadURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("the download url is empty")
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

func ensureBinary(ctx context.Context, data, name, src string, force bool) (bool, error) {
	marker := sourceMarker(data, name)
	installed, err := os.ReadFile(marker)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	// force means force: it is only ever set by "Upgrade now" on a host's own
	// page, and the marker records where a binary came from, not what was in it.
	// A dev artifact server hands out a stable url whose contents change on every
	// build, so skipping the download because the url matches makes that button
	// do nothing at all.
	_, statErr := os.Stat(serviceBinary(name))
	if statErr == nil && !force {
		if strings.TrimSpace(string(installed)) != src {
			log.Printf("%s is installed from a different build than %s; upgrade it from the control server to move it",
				name, src)
		}
		return false, nil
	}

	if err := downloadBinary(ctx, src, serviceBinary(name), name); err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return false, err
	}
	return true, os.WriteFile(marker, []byte(src), 0o600)
}

// want is the file name to pull out of the tarball; it is not always the base of
// dst, which can be a staging path.
func downloadBinary(ctx context.Context, src, dst, want string) error {
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
		return installFromTarball(src, resp.Body, dst, want)
	}
	return installFromReader(resp.Body, dst)
}

func isTarball(src string) bool {
	p := src
	if u, err := url.Parse(src); err == nil {
		p = u.Path
	}
	p = strings.ToLower(p)
	return strings.HasSuffix(p, ".tar.gz") || strings.HasSuffix(p, ".tgz")
}

func installFromTarball(src string, body io.Reader, dst, want string) error {
	gz, err := gzip.NewReader(body)
	if err != nil {
		return fmt.Errorf("%s is not a gzip archive: %w", src, err)
	}
	defer gz.Close()

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

	if err := os.Remove(serviceBinary(guestInitService)); err == nil {
		fmt.Println("removed " + serviceBinary(guestInitService))
	} else if !os.IsNotExist(err) {
		fmt.Println(err)
	}
}
