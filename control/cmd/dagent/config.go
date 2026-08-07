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

  Features Features      `yaml:"features"`
  Network  NetworkConfig `yaml:"network"`
}

// Features is every part of `serve` that changes something outside dagent's own
// data directory: kernel settings, device permissions, the packet filter,
// another daemon's firewall chain, a container, a listening socket on the
// gateway. All of it is off unless the operator turned it on, so a dagent
// started with no configuration -- or with a configuration that only sets up
// the control link -- touches nothing.
//
// The zero value is therefore the safe value, which is also what an empty or
// missing config file yields.
type Features struct {
  // IPForward sets net.ipv4.ip_forward. Host-wide, and other things on the box
  // may be relying on its current value either way.
  IPForward bool `yaml:"ip_forward"`

  // KVMAccess chowns and chmods /dev/kvm so an unprivileged uid can open it,
  // creating the kvm group if it is missing.
  KVMAccess bool `yaml:"kvm_access"`

  // Nftables installs and then continuously reconciles the inet table. Without
  // it nothing repairs drift, and the reconciler's per-VM tap and shaping
  // cleanup does not run either.
  Nftables bool `yaml:"nftables"`

  // DockerCompat adds the accepts to Docker's DOCKER-USER chain that VM traffic
  // needs to survive Docker's FORWARD policy.
  DockerCompat bool `yaml:"docker_compat"`

  // Suricata queues allowed egress to a Suricata container rather than
  // accepting it outright, and starts that container.
  Suricata bool `yaml:"suricata"`

  // DHCP serves leases to guests. A /32 guest cannot install a default route
  // without it, so a host with VMs that expect one wants this on.
  DHCP bool `yaml:"dhcp"`

  // Metadata serves per-VM identity on the gateway address.
  Metadata bool `yaml:"metadata"`
}

// NetworkConfig mirrors netConfig, in the shape an operator writes rather than
// the shape the reconciler uses. The two former toggles here, `suricata` and
// `no_docker_compat`, moved to features; they are still parsed so that a config
// written for an older build gets told rather than silently changing behaviour.
type NetworkConfig struct {
  Pool    string `yaml:"pool"`
  Gateway string `yaml:"gateway"`
  Uplink  string `yaml:"uplink"`
  DNS     string `yaml:"dns"`
  Queues  uint16 `yaml:"queues"`

  Suricata       *bool `yaml:"suricata"`
  NoDockerCompat *bool `yaml:"no_docker_compat"`
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
    warnIfMoved(path, cfg)
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

// warnIfMoved reports settings that used to live under network: and are now
// features. Ignoring them silently would take Suricata off a host that thinks
// it still has it, which is the kind of change an operator has to be told about.
func warnIfMoved(path string, cfg Config) {
  if cfg.Network.Suricata != nil {
    log.Printf("WARNING: %s sets network.suricata, which moved to features.suricata and is being ignored", path)
  }
  if cfg.Network.NoDockerCompat != nil {
    log.Printf("WARNING: %s sets network.no_docker_compat, which is now features.docker_compat (off by default) and is being ignored", path)
  }
}

// netConfigFrom converts the operator's configuration into the form the
// reconciler works with, resolving the uplink if it was left unset. The two
// behavioural flags come from features, so there is one place an operator turns
// each of them on.
func (c Config) netConfig() (netConfig, error) {
  n := netConfig{
    Pool:           c.Network.Pool,
    Gateway:        c.Network.Gateway,
    Uplink:         c.Network.Uplink,
    DNS:            c.Network.DNS,
    Queues:         c.Network.Queues,
    Suricata:       c.Features.Suricata,
    NoDockerCompat: !c.Features.DockerCompat,
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
