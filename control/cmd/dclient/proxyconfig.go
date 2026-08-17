package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"control/internal/proto"
)

// proxy.yaml is written by the control server, not by this host: its ssh user
// list maps a public key to the VM its owner created, and who owns which VM is
// only knowable in the control plane. dclient's job is to put the file down and
// restart the service, in that order and atomically.
//
// The file is replaced whole rather than merged. A merge here would be a second
// opinion about the routing table in the one place that cannot be audited from
// the control plane -- and there would be no way to express a removal, since a
// merged-in entry looks the same as one that was never taken out.

// applyProxyConfig installs a pushed proxy.yaml -- and the key its auth block
// names -- and restarts proxy if either changed.
//
// It reports whether anything changed, so the caller can say so. An identical
// file is not written and the service is not restarted: the server sends one on
// every connect and on every inventory tick, and restarting proxy each time
// would drop every live ssh session on the host every few seconds.
func applyProxyConfig(ctx context.Context, cfg proto.ProxyConfig) (bool, error) {
	// Read here rather than passed in: `dclient connect` does not otherwise hold
	// the config, and the answer only matters when a file arrives.
	//
	// With proxy disabled there is no unit to restart and nothing reads the file,
	// so writing it would leave a routing table on disk that nothing serves and
	// that would take effect the day someone enabled the feature. Saying so is
	// more useful than silently half-doing it.
	dcfg, err := loadConfig("")
	if err != nil {
		return false, fmt.Errorf("could not read the dclient config: %w", err)
	}
	if !dcfg.Proxy.Enable {
		return false, errors.New("proxy is not enabled on this host, so the config was not installed")
	}

	config := cfg.Config
	if !strings.HasSuffix(config, "\n") {
		config += "\n"
	}

	// The key first: the config about to be written names the file, so a proxy
	// restarted between the two would come up pointing at a key that is not there
	// -- or, on a rotation, at the previous one.
	secretChanged, err := writeProxyCookieSecret(cfg.CookieSecretPath, cfg.CookieSecret)
	if err != nil {
		return false, err
	}

	path := serviceConfigPath(proxyService)
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("could not read %s: %w", path, err)
	}
	configChanged := err != nil || string(existing) != config
	if !configChanged && !secretChanged {
		return false, nil
	}

	if configChanged {
		if err := writeFileAtomic(path, []byte(config), 0o644); err != nil {
			return false, err
		}
	}

	// restart, not reload: proxy holds listening sockets and reads its config once
	// at start. The unit sets reuseport, so the new process can bind before the old
	// one is gone.
	if err := systemctl(ctx, "restart", proxyService+".service"); err != nil {
		// The file is already down. Left in place deliberately: the next start picks
		// it up, whereas rolling it back would leave the host serving a routing
		// table the control plane believes it replaced.
		return true, fmt.Errorf("wrote %s but could not restart %s: %w", path, proxyService, err)
	}
	return true, nil
}

// writeProxyCookieSecret puts the key the control server signs login tokens with
// where the auth block in proxy.yaml says it is. It reports whether the file
// changed, so the caller restarts proxy on a rotation even when the config it
// arrived with is byte-identical -- proxy reads the key once, at start.
//
// An empty secret leaves any existing file alone rather than truncating it: it
// means the control server has no key configured, and the config it sent carries
// no auth block, so there is nothing on this host that would read the file.
// Truncating it would turn "auth is off" into "auth is on with an empty key" the
// moment someone configured the server again.
//
// The value is written verbatim -- no trailing newline, no re-encoding. It is
// used as raw bytes on both ends, so a byte added here is a byte the control
// server did not sign with, and every token would fail to verify.
func writeProxyCookieSecret(path, secret string) (bool, error) {
	if secret == "" {
		return false, nil
	}
	if path == "" {
		// Older control servers send the key without saying where it goes. The
		// default is the one this fleet's configs name.
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
	// 0700 for the same reason the ssh key directory is: what is in here is what
	// a session on any guest of this host can be forged with.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, fmt.Errorf("could not create %s: %w", dir, err)
	}
	// 0600 from the moment it exists -- writeNewFile stages through a CreateTemp,
	// which is 0600 before the rename. The error deliberately does not carry the
	// value, and neither does any log line on this path.
	if err := writeNewFile(path, []byte(secret), 0o600); err != nil {
		return false, fmt.Errorf("could not write the proxy cookie secret to %s: %w", path, err)
	}
	log.Printf("wrote the proxy cookie secret to %s", path)
	return true, nil
}

// writeFileAtomic writes through a temporary file in the same directory and
// renames, so a reader -- or a proxy started by an unlucky race -- never sees a
// half-written config.
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
	defer func() { _ = os.Remove(staged) }() // No-op once the rename succeeds.

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("could not write %s: %w", path, err)
	}
	// CreateTemp makes the file 0600, which is not what a config read by a service
	// should end up as.
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(staged, path)
}
