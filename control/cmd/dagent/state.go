package main

import (
  "encoding/json"
  "errors"
  "io/fs"
  "os"
  "path/filepath"
  "strings"
)

const stateFile = "agent.json"

// state is what survives across restarts. The token is the agent's long-lived
// credential, so the file is written 0600 inside a 0700 directory.
type state struct {
  AgentID    string `json:"agent_id"`
  Token      string `json:"token"`
  ControlURL string `json:"control_url"`
}

// defaultStateDir prefers /etc/dagent (the normal home for a system service)
// and falls back to the user's config dir when /etc is not writable, so the
// agent is still usable when run unprivileged during development.
func defaultStateDir() string {
  const system = "/etc/dagent"
  if err := os.MkdirAll(system, 0o700); err == nil {
    if f, err := os.CreateTemp(system, ".writable-*"); err == nil {
      name := f.Name()
      _ = f.Close()
      _ = os.Remove(name)
      return system
    }
  }
  if dir, err := os.UserConfigDir(); err == nil {
    return filepath.Join(dir, "dagent")
  }
  return ".dagent"
}

// loadState returns a zero state (no error) when nothing has been enrolled yet.
func loadState(dir string) (state, error) {
  var s state
  b, err := os.ReadFile(filepath.Join(dir, stateFile))
  if err != nil {
    if errors.Is(err, fs.ErrNotExist) {
      return s, nil
    }
    return s, err
  }
  if err := json.Unmarshal(b, &s); err != nil {
    return state{}, err
  }
  return s, nil
}

// saveState writes atomically: a truncated state file would mean re-enrolling,
// which burns a use of a use-limited key.
func saveState(dir string, s state) error {
  if err := os.MkdirAll(dir, 0o700); err != nil {
    return err
  }
  b, err := json.MarshalIndent(s, "", "  ")
  if err != nil {
    return err
  }

  tmp, err := os.CreateTemp(dir, ".agent-*.json")
  if err != nil {
    return err
  }
  tmpName := tmp.Name()
  defer func() { _ = os.Remove(tmpName) }()

  if err := tmp.Chmod(0o600); err != nil {
    _ = tmp.Close()
    return err
  }
  if _, err := tmp.Write(b); err != nil {
    _ = tmp.Close()
    return err
  }
  if err := tmp.Close(); err != nil {
    return err
  }
  return os.Rename(tmpName, filepath.Join(dir, stateFile))
}

// machineID identifies the box across reinstalls of the agent itself. Both
// paths are the standard systemd/D-Bus locations; hostname is a last resort
// because it is not stable.
func machineID() string {
  for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
    if b, err := os.ReadFile(p); err == nil {
      if id := strings.TrimSpace(string(b)); id != "" {
        return id
      }
    }
  }
  if h, err := os.Hostname(); err == nil {
    return "hostname:" + h
  }
  return ""
}
