package main

import (
  "context"
  "errors"
  "fmt"
  "os"
  "path/filepath"
  "strings"
)

// proxy.yaml is written by the control server, not by this host: its ssh user
// list maps a public key to the VM its owner created, and who owns which VM is
// only knowable in the control plane. dagent's job is to put the file down and
// restart the service, in that order and atomically.
//
// The file is replaced whole rather than merged. A merge here would be a second
// opinion about the routing table in the one place that cannot be audited from
// the control plane -- and there would be no way to express a removal, since a
// merged-in entry looks the same as one that was never taken out.

// applyProxyConfig installs a pushed proxy.yaml and restarts proxy if it changed.
//
// It reports whether anything changed, so the caller can say so. An identical
// file is not written and the service is not restarted: the server sends one on
// every connect and on every inventory tick, and restarting proxy each time
// would drop every live ssh session on the host every few seconds.
func applyProxyConfig(ctx context.Context, config string) (bool, error) {
  // Read here rather than passed in: `dagent connect` does not otherwise hold
  // the config, and the answer only matters when a file arrives.
  //
  // With proxy disabled there is no unit to restart and nothing reads the file,
  // so writing it would leave a routing table on disk that nothing serves and
  // that would take effect the day someone enabled the feature. Saying so is
  // more useful than silently half-doing it.
  cfg, err := loadConfig("")
  if err != nil {
    return false, fmt.Errorf("could not read the dagent config: %w", err)
  }
  if !cfg.Proxy.Enable {
    return false, errors.New("proxy is not enabled on this host, so the config was not installed")
  }

  if !strings.HasSuffix(config, "\n") {
    config += "\n"
  }

  path := serviceConfigPath(proxyService)
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
