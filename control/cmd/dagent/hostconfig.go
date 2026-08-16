package main

import (
  "context"
  "errors"
  "fmt"
  "log"
  "os"
  "os/exec"
  "strings"
)

// suricata.yaml and dpipe.yaml arrive from the control server whole, like
// proxy.yaml and vector.yaml before them. Neither is merged: the file the
// server sent is the file the host runs.
//
// Both are more disruptive to restart than proxy is, so both are written only
// when the content actually differs. The server sends one of each on every
// connect, and it is normal for that to be a no-op.

// localPool is the VM subnet this host was configured with. Reported in the
// hello frame so the server can put it in HOME_NET, which is the one thing in
// suricata.yaml the control plane cannot know.
//
// "" when the config cannot be read: the server treats that as "do not send a
// config" rather than substituting a default, which is the right way round --
// a wrong HOME_NET silently stops every $HOME_NET rule from matching.
func localPool() string {
  cfg, err := loadConfig("")
  if err != nil {
    log.Printf("could not read the dagent config to report the vm pool: %v", err)
    return ""
  }
  return cfg.Network.Pool
}

// applySuricataConfig installs a pushed suricata.yaml and restarts the
// container if it changed.
//
// A restart, not a reload: Suricata reads this file once at start, and
// suricatasc has no equivalent of reload-rules for it. That is expensive --
// every NFQUEUE binding goes away for as long as the process takes to come
// back, and packets hashed to an unbound queue are dropped -- which is exactly
// why an identical file is not written and nothing is restarted for it.
func applySuricataConfig(ctx context.Context, config string) (bool, error) {
  cfg, err := loadConfig("")
  if err != nil {
    return false, fmt.Errorf("could not read the dagent config: %w", err)
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

  // Only if something is running. On a host whose container is down, the
  // reconcile pass starts it with the file that was just written, and a restart
  // of nothing would be an error for no reason.
  docker, derr := exec.LookPath("docker")
  if derr != nil {
    return true, nil
  }
  if containerState(docker, suricataContainer) != "running" {
    return true, nil
  }
  if out, rerr := exec.CommandContext(ctx, docker, "restart", suricataContainer).CombinedOutput(); rerr != nil {
    // The file is already down and is what the next start will read, so it is
    // left in place: rolling it back would leave the host enforcing a config
    // the control plane believes it replaced.
    return true, fmt.Errorf("wrote %s but could not restart suricata: %v: %s",
      path, rerr, strings.TrimSpace(string(out)))
  }
  return true, nil
}

// applyDpipeConfig installs a pushed dpipe.yaml and restarts dpipe if it
// changed. Restarting drops every live ssh session and browser terminal on the
// host, which is the whole reason this compares before writing.
func applyDpipeConfig(ctx context.Context, config string) (bool, error) {
  cfg, err := loadConfig("")
  if err != nil {
    return false, fmt.Errorf("could not read the dagent config: %w", err)
  }
  if !cfg.Dpipe.Enable {
    return false, errors.New("dpipe is not enabled on this host, so the config was not installed")
  }

  if !strings.HasSuffix(config, "\n") {
    config += "\n"
  }
  path := serviceConfigPath(dpipeService)
  existing, err := os.ReadFile(path)
  if err != nil && !os.IsNotExist(err) {
    return false, fmt.Errorf("could not read %s: %w", path, err)
  }
  if err == nil && string(existing) == config {
    return false, nil
  }

  // The config turns ssh on and names both key files, so it must not land
  // before they exist or the restart below is a restart loop.
  if err := ensureDpipeKeys(); err != nil {
    return false, err
  }
  if err := writeFileAtomic(path, []byte(config), 0o644); err != nil {
    return false, err
  }
  if err := systemctl(ctx, "restart", dpipeService+".service"); err != nil {
    return true, fmt.Errorf("wrote %s but could not restart %s: %w", path, dpipeService, err)
  }
  return true, nil
}
