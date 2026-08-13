package dpipe

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that unmarshals from a YAML string like "5s".
type Duration time.Duration

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		var i int64
		if err2 := n.Decode(&i); err2 != nil {
			return err
		}
		*d = Duration(time.Duration(i) * time.Second)
		return nil
	}
	if s == "" {
		*d = 0
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// D returns the duration value.
func (d Duration) D() time.Duration { return time.Duration(d) }

// Or returns d, or def when d is zero.
func (d Duration) Or(def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return time.Duration(d)
}

// Config is the dpipe configuration file.
type Config struct {
	ControlSocket string   `yaml:"control_socket"`
	UpgradeSocket string   `yaml:"upgrade_socket"`
	DrainTimeout  Duration `yaml:"drain_timeout"`
	LogLevel      string   `yaml:"log_level"`

	SSH     SSHConfig     `yaml:"ssh"`
	TLS     TLSConfig     `yaml:"tls"`
	Console ConsoleConfig `yaml:"console"`
}

// ConsoleConfig configures the browser terminal. Who may open one is the proxy's
// decision and is never revisited here; these are the resource limits dpipe puts
// on what it is handed.
type ConsoleConfig struct {
	Enabled bool `yaml:"enabled"`
	// IdleTimeout ends a session with no traffic in either direction. A terminal
	// holds an SSH connection and a pty in the guest open for as long as the tab
	// exists, which is otherwise forever.
	IdleTimeout Duration `yaml:"idle_timeout"`
	// MaxSessionsPerHost caps concurrent terminals per VM. Per VM rather than
	// global so one guest's open tabs cannot lock every other guest out.
	MaxSessionsPerHost int `yaml:"max_sessions_per_host"`
}

const (
	defaultConsoleIdleTimeout = 30 * time.Minute
	defaultConsoleMaxPerHost  = 3
)

func (c ConsoleConfig) idleTimeout() time.Duration {
	return c.IdleTimeout.Or(defaultConsoleIdleTimeout)
}

func (c ConsoleConfig) maxPerHost() int {
	if c.MaxSessionsPerHost <= 0 {
		return defaultConsoleMaxPerHost
	}
	return c.MaxSessionsPerHost
}

// SSHConfig configures SSH termination.
type SSHConfig struct {
	Enabled           bool     `yaml:"enabled"`
	HostKey           string   `yaml:"host_key"`
	ClientKey         string   `yaml:"client_key"`
	BackendKnownHosts string   `yaml:"backend_known_hosts"`
	DialTimeout       Duration `yaml:"dial_timeout"`
	ResolveTimeout    Duration `yaml:"resolve_timeout"`
}

// CertConfig is one SNI-selected certificate.
type CertConfig struct {
	SNI  string `yaml:"sni"`
	Cert string `yaml:"cert"`
	Key  string `yaml:"key"`
}

// TLSConfig configures TLS termination.
type TLSConfig struct {
	Enabled     bool         `yaml:"enabled"`
	Certs       []CertConfig `yaml:"certs"`
	DefaultCert string       `yaml:"default_cert"`
	DefaultKey  string       `yaml:"default_key"`
	MinVersion  string       `yaml:"min_version"`

	DialTimeout    Duration `yaml:"dial_timeout"`
	ResolveTimeout Duration `yaml:"resolve_timeout"`
	SniffTimeout   Duration `yaml:"sniff_timeout"`
	SniffMaxBytes  int      `yaml:"sniff_max_bytes"`
}

// LoadConfig reads and validates a configuration file.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) validate() error {
	if c.ControlSocket == "" {
		return errors.New("config: control_socket is required")
	}
	if c.UpgradeSocket == "" {
		return errors.New("config: upgrade_socket is required")
	}
	if c.ControlSocket == c.UpgradeSocket {
		return errors.New("config: control_socket and upgrade_socket must differ")
	}
	if c.SSH.Enabled {
		if c.SSH.HostKey == "" || c.SSH.ClientKey == "" {
			return errors.New("config: ssh.enabled requires host_key and client_key")
		}
	}
	// The console opens an SSH session to the guest with dpipe's client key under
	// the same host-key policy as the SSH ingress, so without ssh: there is no
	// key to authenticate with and nothing to verify the guest against.
	if c.Console.Enabled && !c.SSH.Enabled {
		return errors.New("config: console.enabled requires ssh.enabled (the console opens its shell with dpipe's ssh client key)")
	}
	if c.TLS.Enabled {
		if len(c.TLS.Certs) == 0 && (c.TLS.DefaultCert == "" || c.TLS.DefaultKey == "") {
			return errors.New("config: tls.enabled requires certs[] or default_cert/default_key")
		}
		for i, cc := range c.TLS.Certs {
			if cc.SNI == "" || cc.Cert == "" || cc.Key == "" {
				return fmt.Errorf("config: tls.certs[%d] needs sni, cert and key", i)
			}
		}
		if _, err := tlsMinVersion(c.TLS.MinVersion); err != nil {
			return err
		}
	}
	return nil
}

func tlsMinVersion(s string) (uint16, error) {
	switch s {
	case "", "1.2":
		return tls.VersionTLS12, nil
	case "1.3":
		return tls.VersionTLS13, nil
	}
	return 0, fmt.Errorf("config: tls.min_version must be 1.2 or 1.3, got %q", s)
}

// material holds everything loaded from disk at startup.
type material struct {
	hostSigner   ssh.Signer
	clientSigner ssh.Signer
	hostKeyCB    ssh.HostKeyCallback

	certs      map[string]*tls.Certificate
	defaultCrt *tls.Certificate
	tlsMin     uint16
}

// loadMaterial loads SSH keys and TLS certificates according to the config.
func loadMaterial(c *Config, log *slog.Logger) (*material, error) {
	m := &material{certs: map[string]*tls.Certificate{}}

	if c.SSH.Enabled {
		hs, err := loadSigner(c.SSH.HostKey)
		if err != nil {
			return nil, fmt.Errorf("ssh.host_key: %w", err)
		}
		cs, err := loadSigner(c.SSH.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("ssh.client_key: %w", err)
		}
		m.hostSigner, m.clientSigner = hs, cs

		if c.SSH.BackendKnownHosts == "" {
			log.Warn("ssh.backend_known_hosts is empty: backend host keys are NOT verified (development only)")
			m.hostKeyCB = ssh.InsecureIgnoreHostKey()
		} else {
			cb, err := knownhosts.New(c.SSH.BackendKnownHosts)
			if err != nil {
				return nil, fmt.Errorf("ssh.backend_known_hosts: %w", err)
			}
			m.hostKeyCB = cb
		}
	}

	if c.TLS.Enabled {
		min, err := tlsMinVersion(c.TLS.MinVersion)
		if err != nil {
			return nil, err
		}
		m.tlsMin = min
		for _, cc := range c.TLS.Certs {
			crt, err := tls.LoadX509KeyPair(cc.Cert, cc.Key)
			if err != nil {
				return nil, fmt.Errorf("tls.certs[%s]: %w", cc.SNI, err)
			}
			m.certs[normalizeSNI(cc.SNI)] = &crt
		}
		if c.TLS.DefaultCert != "" && c.TLS.DefaultKey != "" {
			crt, err := tls.LoadX509KeyPair(c.TLS.DefaultCert, c.TLS.DefaultKey)
			if err != nil {
				return nil, fmt.Errorf("tls.default_cert: %w", err)
			}
			m.defaultCrt = &crt
		}
		if len(m.certs) == 0 && m.defaultCrt == nil {
			return nil, errors.New("tls.enabled but no usable certificate loaded")
		}
	}

	return m, nil
}

func loadSigner(path string) (ssh.Signer, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(b)
}

// ParseLogLevel maps a config log level to a slog level.
func ParseLogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "", "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}
