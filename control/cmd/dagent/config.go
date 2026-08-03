package main

import (
  "fmt"
  "log"
  "os"
  "path/filepath"

  "gopkg.in/yaml.v3"
)

// Config is the host's operator-set configuration. It is the source of truth
// for the daemon; net.json remains only as the resolved copy the direct
// (no-daemon, root) path reads.
//
// The file holds the enrollment key, so it is expected to be 0600 root:root.
// Membership of the dagent group grants the socket, deliberately not this.
type Config struct {
  // Control plane. EnrollmentKey is only consulted the first time: once
  // agent.json has a token, the key is ignored.
  ControlURL    string `yaml:"control_url"`
  EnrollmentKey string `yaml:"enrollment_key"`
  Insecure      bool   `yaml:"insecure"`

  DataDir string `yaml:"data_dir"`
  Socket  string `yaml:"socket"`
  Group   string `yaml:"group"`

  Network NetworkConfig `yaml:"network"`
}

// NetworkConfig mirrors netConfig, in the shape an operator writes rather than
// the shape the reconciler uses.
type NetworkConfig struct {
  Pool           string `yaml:"pool"`
  Gateway        string `yaml:"gateway"`
  Uplink         string `yaml:"uplink"`
  DNS            string `yaml:"dns"`
  Suricata       bool   `yaml:"suricata"`
  Queues         uint16 `yaml:"queues"`
  NoDockerCompat bool   `yaml:"no_docker_compat"`
}

const (
  configPath    = "/etc/dagent/config.yaml"
  defaultSocket = "/run/dagent/dagent.sock"
  defaultGroup  = "dagent"
)

// loadConfig reads the config file, filling in defaults. A missing file is not
// an error: every field has a usable default, and a host with no control server
// needs no configuration at all.
func loadConfig(path string) (Config, error) {
  var cfg Config
  if path == "" {
    path = configPath
  }

  b, err := os.ReadFile(path)
  switch {
  case err == nil:
    if err := yaml.Unmarshal(b, &cfg); err != nil {
      return cfg, fmt.Errorf("%s: %w", path, err)
    }
    warnIfReadable(path, cfg)
  case os.IsPermission(err):
    // Expected for the CLI: the file is root-only because it holds the
    // enrollment key, and the client only needs the socket path, which has a
    // default. DAGENT_SOCKET covers the case where an operator moved it.
  case !os.IsNotExist(err):
    return cfg, err
  }

  if s := os.Getenv("DAGENT_SOCKET"); s != "" {
    cfg.Socket = s
  }

  if cfg.DataDir == "" {
    cfg.DataDir = defaultDataDir()
  }
  if cfg.Socket == "" {
    cfg.Socket = defaultSocket
  }
  if cfg.Group == "" {
    cfg.Group = defaultGroup
  }
  if cfg.Network.Pool == "" {
    cfg.Network.Pool = defaultPool
  }
  if cfg.Network.Gateway == "" {
    cfg.Network.Gateway = defaultGateway
  }
  if cfg.Network.DNS == "" {
    cfg.Network.DNS = defaultDNS
  }
  if cfg.Network.Queues == 0 {
    cfg.Network.Queues = defaultQueues
  }
  return cfg, nil
}

// warnIfReadable is a nudge, not a refusal: an operator who has deliberately
// loosened the permissions should not be locked out of their own host.
func warnIfReadable(path string, cfg Config) {
  if cfg.EnrollmentKey == "" {
    return
  }
  info, err := os.Stat(path)
  if err != nil {
    return
  }
  if info.Mode().Perm()&0o077 != 0 {
    log.Printf("WARNING: %s holds an enrollment key and is mode %04o; chmod 600 it",
      path, info.Mode().Perm())
  }
}

// netConfigFrom converts the operator's configuration into the form the
// reconciler works with, resolving the uplink if it was left unset.
func (c Config) netConfig() (netConfig, error) {
  n := netConfig{
    Pool:           c.Network.Pool,
    Gateway:        c.Network.Gateway,
    Uplink:         c.Network.Uplink,
    DNS:            c.Network.DNS,
    Suricata:       c.Network.Suricata,
    Queues:         c.Network.Queues,
    NoDockerCompat: c.Network.NoDockerCompat,
  }
  if n.Uplink == "" {
    var err error
    if n.Uplink, err = defaultUplink(); err != nil {
      return n, err
    }
  }
  return n, nil
}

// socketDir is created before the listener binds; systemd's RuntimeDirectory
// would also do it, but the daemon should not depend on being run by systemd.
func socketDir(socket string) string { return filepath.Dir(socket) }
