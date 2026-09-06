package intproxy

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

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

func (d Duration) D() time.Duration { return time.Duration(d) }

func (d Duration) Or(def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return time.Duration(d)
}

type Config struct {
	Listen    string `yaml:"listen"`
	Freebind  bool   `yaml:"freebind"`
	Reuseport bool   `yaml:"reuseport"`

	TLD   string `yaml:"tld"`
	Label string `yaml:"label"`

	ConsoleURL string `yaml:"console_url"`

	TLS    TLSConfig    `yaml:"tls"`
	Broker BrokerConfig `yaml:"broker"`
	GitHub GitHubConfig `yaml:"github"`

	DialTimeout           Duration `yaml:"dial_timeout"`
	TLSHandshakeTimeout   Duration `yaml:"tls_handshake_timeout"`
	ResponseHeaderTimeout Duration `yaml:"response_header_timeout"`
	IdleTimeout           Duration `yaml:"idle_timeout"`
	ReadHeaderTimeout     Duration `yaml:"read_header_timeout"`
	ShutdownGrace         Duration `yaml:"shutdown_grace"`

	LogLevel string `yaml:"log_level"`
}

type TLSConfig struct {
	// Enabled defaults to true: a proxy that injects credentials should not
	// fall back to plaintext because a field was left out of a file.
	Enabled    *bool  `yaml:"enabled"`
	Cert       string `yaml:"cert"`
	Key        string `yaml:"key"`
	MinVersion string `yaml:"min_version"`
}

func (t TLSConfig) enabled() bool {
	return t.Enabled == nil || *t.Enabled
}

type BrokerConfig struct {
	Socket    string   `yaml:"socket"`
	Timeout   Duration `yaml:"timeout"`
	TokenSkew Duration `yaml:"token_skew"`
}

type GitHubConfig struct {
	GitHost string `yaml:"git_host"`
	APIHost string `yaml:"api_host"`
}

const (
	defaultGitHost = "github.com"
	defaultAPIHost = "api.github.com"
	defaultLabel   = "int"
)

func (g GitHubConfig) gitHost() string {
	if g.GitHost == "" {
		return defaultGitHost
	}
	return g.GitHost
}

func (g GitHubConfig) apiHost() string {
	if g.APIHost == "" {
		return defaultAPIHost
	}
	return g.APIHost
}

func (c *Config) label() string {
	if c.Label == "" {
		return defaultLabel
	}
	return c.Label
}

func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

var (
	hostnamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
	labelPattern    = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
)

func (c *Config) Validate() error {
	if err := validHostPort("listen", c.Listen); err != nil {
		return err
	}
	if c.TLD == "" {
		return errors.New("config: tld is required")
	}
	if !hostnamePattern.MatchString(c.TLD) {
		return fmt.Errorf("config: tld %q is not a hostname", c.TLD)
	}
	if !labelPattern.MatchString(c.label()) {
		return fmt.Errorf("config: label %q must be a single hostname label", c.label())
	}
	if c.TLS.enabled() {
		if c.TLS.Cert == "" || c.TLS.Key == "" {
			return errors.New("config: tls.cert and tls.key are both required unless tls.enabled is false")
		}
		switch c.TLS.MinVersion {
		case "", "1.2", "1.3":
		default:
			return fmt.Errorf("config: tls.min_version %q must be 1.2 or 1.3", c.TLS.MinVersion)
		}
	}
	if c.Broker.Socket == "" {
		return errors.New("config: broker.socket is required")
	}
	if !filepath.IsAbs(c.Broker.Socket) {
		return fmt.Errorf("config: broker.socket %q must be an absolute path", c.Broker.Socket)
	}
	return nil
}

// validHostPort rejects a wildcard host: intproxy shares :443 with dproxy and
// only coexists with it by binding one concrete address.
func validHostPort(field, addr string) error {
	if addr == "" {
		return fmt.Errorf("config: %s is required", field)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("config: %s %q must be host:port: %w", field, addr, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" || strings.Contains(host, "*") {
		return fmt.Errorf("config: %s %q needs a concrete address, not a wildcard", field, addr)
	}
	if net.ParseIP(host) == nil {
		return fmt.Errorf("config: %s %q needs an IP address", field, addr)
	}
	p, err := strconv.Atoi(port)
	if err != nil || p <= 0 || p > 65535 {
		return fmt.Errorf("config: %s %q has an invalid port", field, addr)
	}
	return nil
}

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
