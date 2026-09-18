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

	"intproxy/internal/credential"
	"intproxy/internal/integration"
	"intproxy/internal/policy"
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

// Credential modes. Broker holds nothing locally and asks a unix socket, which
// is how a managed fleet runs it. Local holds the tokens and the policy in this
// process, which is how a standalone deployment runs it.
const (
	ModeBroker = "broker"
	ModeLocal  = "local"
)

type Config struct {
	Listen    string `yaml:"listen"`
	Freebind  bool   `yaml:"freebind"`
	Reuseport bool   `yaml:"reuseport"`

	TLD   string `yaml:"tld"`
	Label string `yaml:"label"`

	// DocsURL is where a refused caller is pointed.
	DocsURL string `yaml:"docs_url"`

	TLS          TLSConfig           `yaml:"tls"`
	Credential   CredentialConfig    `yaml:"credential"`
	Integrations []IntegrationConfig `yaml:"integrations"`
	Policy       *policy.Policy      `yaml:"policy"`

	DialTimeout           Duration `yaml:"dial_timeout"`
	TLSHandshakeTimeout   Duration `yaml:"tls_handshake_timeout"`
	ResponseHeaderTimeout Duration `yaml:"response_header_timeout"`
	IdleTimeout           Duration `yaml:"idle_timeout"`
	ReadHeaderTimeout     Duration `yaml:"read_header_timeout"`
	ShutdownGrace         Duration `yaml:"shutdown_grace"`

	LogLevel string `yaml:"log_level"`

	path string
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

type CredentialConfig struct {
	Mode      string   `yaml:"mode"`
	TokenSkew Duration `yaml:"token_skew"`
	// MaxAge caps how long a granted credential is reused before the source is
	// asked again. It is what bounds revocation: nothing pushes a change here,
	// so without it a revoked grant would keep working until its token expired.
	MaxAge Duration     `yaml:"max_age"`
	Broker BrokerConfig `yaml:"broker"`
}

type BrokerConfig struct {
	Socket  string   `yaml:"socket"`
	Timeout Duration `yaml:"timeout"`
}

// IntegrationConfig keeps the whole node so an integration decodes its own
// options. Adding one therefore adds no fields to this struct.
type IntegrationConfig struct {
	Name    string
	Enabled bool
	Auth    credential.StaticConfig

	node yaml.Node
}

func (i *IntegrationConfig) UnmarshalYAML(n *yaml.Node) error {
	var head struct {
		Name    string                  `yaml:"name"`
		Enabled *bool                   `yaml:"enabled"`
		Auth    credential.StaticConfig `yaml:"auth"`
	}
	if err := n.Decode(&head); err != nil {
		return err
	}
	i.Name = head.Name
	i.Enabled = head.Enabled == nil || *head.Enabled
	i.Auth = head.Auth
	i.node = *n
	return nil
}

func (i IntegrationConfig) Decode(v any) error { return i.node.Decode(v) }

const defaultLabel = "int"

func (c *Config) label() string {
	if c.Label == "" {
		return defaultLabel
	}
	return c.Label
}

// hostname is the vhost an integration answers for.
func (c *Config) hostname(name string) string {
	return name + "." + c.label() + "." + c.TLD
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
	c.path = path
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
	if err := c.validateListen(); err != nil {
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
	if err := c.validateIntegrations(); err != nil {
		return err
	}
	return c.validateCredential()
}

// validateListen only demands a concrete address when reuseport is on. That
// combination means another process holds the same port on this host and the
// two coexist because this bind is the more specific one; a wildcard would
// collide with it instead. On its own, a wildcard is perfectly ordinary.
func (c *Config) validateListen() error {
	if c.Listen == "" {
		return errors.New("config: listen is required")
	}
	host, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("config: listen %q must be host:port: %w", c.Listen, err)
	}
	p, err := strconv.Atoi(port)
	if err != nil || p <= 0 || p > 65535 {
		return fmt.Errorf("config: listen %q has an invalid port", c.Listen)
	}
	if !c.Reuseport {
		return nil
	}
	if host == "" || host == "0.0.0.0" || host == "::" || strings.Contains(host, "*") {
		return fmt.Errorf("config: listen %q needs a concrete address, not a wildcard, when reuseport is on", c.Listen)
	}
	if net.ParseIP(host) == nil {
		return fmt.Errorf("config: listen %q needs an IP address when reuseport is on", c.Listen)
	}
	return nil
}

func (c *Config) validateIntegrations() error {
	if len(c.Integrations) == 0 {
		return errors.New("config: at least one integration is required")
	}
	seen := map[string]bool{}
	enabled := 0
	for _, ic := range c.Integrations {
		if ic.Name == "" {
			return errors.New("config: an integration has no name")
		}
		if seen[ic.Name] {
			return fmt.Errorf("config: integration %q is listed twice", ic.Name)
		}
		seen[ic.Name] = true
		if !integration.Known(ic.Name) {
			return fmt.Errorf("config: integration %q is not known; this build has %s",
				ic.Name, strings.Join(integration.Names(), ", "))
		}
		if ic.Enabled {
			enabled++
		}
	}
	if enabled == 0 {
		return errors.New("config: every integration is disabled")
	}
	return nil
}

func (c *Config) validateCredential() error {
	switch c.Credential.Mode {
	case ModeBroker:
		if c.Credential.Broker.Socket == "" {
			return errors.New("config: credential.broker.socket is required under mode: broker")
		}
		if !filepath.IsAbs(c.Credential.Broker.Socket) {
			return fmt.Errorf("config: credential.broker.socket %q must be an absolute path", c.Credential.Broker.Socket)
		}
		// A local copy of either would be a second, staler answer to a question
		// the broker already answers on every request.
		if c.Policy != nil {
			return errors.New("config: policy belongs to mode: local; under mode: broker the control server decides")
		}
		for _, ic := range c.Integrations {
			if !ic.Auth.Empty() {
				return fmt.Errorf("config: integration %q sets auth, but mode: broker holds no credentials", ic.Name)
			}
		}
		return nil

	case ModeLocal:
		if c.Credential.Broker.Socket != "" {
			return errors.New("config: credential.broker.socket is set but the mode is local")
		}
		if c.Policy == nil {
			return errors.New("config: policy is required under mode: local, or no client can reach anything")
		}
		for _, ic := range c.Integrations {
			if ic.Enabled && ic.Auth.Empty() {
				return fmt.Errorf("config: integration %q needs an auth block under mode: local", ic.Name)
			}
		}
		return nil

	case "":
		return errors.New(`config: credential.mode is required, either "broker" or "local"`)
	}
	return fmt.Errorf("config: credential.mode %q must be %q or %q", c.Credential.Mode, ModeBroker, ModeLocal)
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
