package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ControlURL    string `yaml:"control_url"`
	EnrollmentKey string `yaml:"enrollment_key"`
	Insecure      bool   `yaml:"insecure"`

	DataDir string `yaml:"data_dir"`
	Socket  string `yaml:"socket"`
	Group   string `yaml:"group"`

	Features Features      `yaml:"features"`
	Network  NetworkConfig `yaml:"network"`

	LegacyDpipe  *yaml.Node `yaml:"dpipe"`
	LegacyProxy  *yaml.Node `yaml:"dproxy"`
	LegacyVector *yaml.Node `yaml:"vector"`
}

type Features struct {
	IPForward bool `yaml:"ip_forward"`

	KVMAccess bool `yaml:"kvm_access"`

	Nftables bool `yaml:"nftables"`

	DockerCompat bool `yaml:"docker_compat"`

	Suricata bool `yaml:"suricata"`

	DHCP bool `yaml:"dhcp"`

	Metadata bool `yaml:"metadata"`
}

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
	configPath    = "/etc/dclient/config.yaml"
	defaultSocket = "/run/dclient/dclient.sock"
	defaultGroup  = "dclient"
)

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
	case !os.IsNotExist(err):
		return cfg, err
	}

	if s := os.Getenv("DCLIENT_SOCKET"); s != "" {
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

func warnIfMoved(path string, cfg Config) {
	if cfg.Network.Suricata != nil {
		log.Printf("WARNING: %s sets network.suricata, which moved to features.suricata and is being ignored", path)
	}
	if cfg.Network.NoDockerCompat != nil {
		log.Printf("WARNING: %s sets network.no_docker_compat, which is now features.docker_compat (off by default) and is being ignored", path)
	}
	for _, moved := range []struct {
		section string
		node    *yaml.Node
	}{
		{"dpipe", cfg.LegacyDpipe},
		{"dproxy", cfg.LegacyProxy},
		{"vector", cfg.LegacyVector},
	} {
		if moved.node != nil {
			log.Printf("WARNING: %s sets %s, which the control server now decides per host and is being ignored",
				path, moved.section)
		}
	}
}

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

func socketDir(socket string) string { return filepath.Dir(socket) }
