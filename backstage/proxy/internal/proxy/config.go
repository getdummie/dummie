package proxy

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

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

// Config is the proxy configuration file.
type Config struct {
	ControlSocket string `yaml:"control_socket"`

	HTTP  *HTTPConfig  `yaml:"http"`
	HTTPS *HTTPSConfig `yaml:"https"`
	TCP   []TCPRoute   `yaml:"tcp"`
	SSH   *SSHConfig   `yaml:"ssh"`

	ListenForwards []ForwardConfig `yaml:"listen_forwards"`

	DialTimeout       Duration `yaml:"dial_timeout"`
	HTTPSniffTimeout  Duration `yaml:"http_sniff_timeout"`
	HTTPSniffMaxBytes int      `yaml:"http_sniff_max_bytes"`
	LogLevel          string   `yaml:"log_level"`
}

// HTTPConfig is the plaintext HTTP ingress.
type HTTPConfig struct {
	Listen    string              `yaml:"listen"`
	Reuseport bool                `yaml:"reuseport"`
	Hosts     map[string]HTTPHost `yaml:"hosts"`
	Default   string              `yaml:"default"`
}

// HTTPHost is one routed hostname: the backend machine and the ports it
// exposes. Requests arriving for the hostname are routed to default_port.
type HTTPHost struct {
	Host        string `yaml:"host"`
	PublicPorts []int  `yaml:"public_ports"`
	DefaultPort int    `yaml:"default_port"`
}

// Target is the backend address requests for this hostname are sent to.
func (h HTTPHost) Target() string {
	return net.JoinHostPort(h.Host, strconv.Itoa(h.DefaultPort))
}

// HTTPSConfig is the TLS ingress. Routing reuses http.hosts via
// resolve{kind:"http"}; the certificates live in dpipe.
type HTTPSConfig struct {
	Listen    string `yaml:"listen"`
	Reuseport bool   `yaml:"reuseport"`
}

// TCPRoute is one opaque TCP ingress listener.
type TCPRoute struct {
	Listen    string `yaml:"listen"`
	Target    string `yaml:"target"`
	Protocol  string `yaml:"protocol"`
	Reuseport bool   `yaml:"reuseport"`
}

// SSHConfig is the SSH ingress plus the pubkey policy answered over resolve.
type SSHConfig struct {
	Listen    string    `yaml:"listen"`
	Reuseport bool      `yaml:"reuseport"`
	Users     []SSHUser `yaml:"users"`
}

// SSHUser maps one authorized public key to a target and remote user.
type SSHUser struct {
	PubKey     string `yaml:"pubkey"`
	PubKeyFile string `yaml:"pubkey_file"`
	Target     string `yaml:"target"`
	RemoteUser string `yaml:"remote_user"`
}

// ForwardConfig is a listen_forward job programmed in dpipe at startup.
type ForwardConfig struct {
	Listen string `yaml:"listen"`
	Target string `yaml:"target"`
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
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate checks the configuration for the invariants the proxy relies on.
func (c *Config) Validate() error {
	if c.ControlSocket == "" {
		return errors.New("config: control_socket is required")
	}

	ingress := 0
	seen := map[string]string{}
	claim := func(kind, addr string) error {
		if addr == "" {
			return fmt.Errorf("config: %s.listen is required", kind)
		}
		if _, _, err := net.SplitHostPort(addr); err != nil {
			return fmt.Errorf("config: %s.listen %q must be host:port: %w", kind, addr, err)
		}
		if prev, dup := seen[addr]; dup {
			return fmt.Errorf("config: listener %s is used by both %s and %s", addr, prev, kind)
		}
		seen[addr] = kind
		return nil
	}

	if c.HTTP != nil {
		if err := claim("http", c.HTTP.Listen); err != nil {
			return err
		}
		ingress++
		for host, h := range c.HTTP.Hosts {
			if host == "" || strings.Contains(host, "*") {
				return fmt.Errorf("config: http.hosts key %q is invalid (no wildcards)", host)
			}
			field := "http.hosts[" + host + "]"
			if h.Host == "" || strings.Contains(h.Host, "*") {
				return fmt.Errorf("config: %s.host %q needs a concrete host", field, h.Host)
			}
			if err := validPort(field+".default_port", h.DefaultPort); err != nil {
				return err
			}
			for i, p := range h.PublicPorts {
				if err := validPort(fmt.Sprintf("%s.public_ports[%d]", field, i), p); err != nil {
					return err
				}
			}
			if len(h.PublicPorts) > 0 && !slices.Contains(h.PublicPorts, h.DefaultPort) {
				return fmt.Errorf("config: %s.default_port %d is not listed in public_ports", field, h.DefaultPort)
			}
		}
		if c.HTTP.Default != "" {
			if err := validHostPort("http.default", c.HTTP.Default); err != nil {
				return err
			}
		}
	}
	if c.HTTPS != nil {
		if err := claim("https", c.HTTPS.Listen); err != nil {
			return err
		}
		ingress++
		if c.HTTP == nil || len(c.HTTP.Hosts) == 0 {
			return errors.New("config: https requires http.hosts (HTTPS routing reuses the host map)")
		}
	}
	for i, r := range c.TCP {
		if err := claim(fmt.Sprintf("tcp[%d]", i), r.Listen); err != nil {
			return err
		}
		ingress++
		if err := validHostPort(fmt.Sprintf("tcp[%d].target", i), r.Target); err != nil {
			return err
		}
		if r.Protocol != "" && r.Protocol != "tcp" {
			return fmt.Errorf("config: tcp[%d].protocol must be tcp", i)
		}
	}
	if c.SSH != nil {
		if err := claim("ssh", c.SSH.Listen); err != nil {
			return err
		}
		ingress++
		if len(c.SSH.Users) == 0 {
			return errors.New("config: ssh requires at least one user")
		}
		for i, u := range c.SSH.Users {
			if (u.PubKey == "") == (u.PubKeyFile == "") {
				return fmt.Errorf("config: ssh.users[%d] needs exactly one of pubkey or pubkey_file", i)
			}
			if u.RemoteUser == "" {
				return fmt.Errorf("config: ssh.users[%d].remote_user is required", i)
			}
			if err := validHostPort(fmt.Sprintf("ssh.users[%d].target", i), u.Target); err != nil {
				return err
			}
		}
	}
	if ingress == 0 {
		return errors.New("config: at least one ingress (http, https, tcp or ssh) is required")
	}

	for i, f := range c.ListenForwards {
		if err := validHostPort(fmt.Sprintf("listen_forwards[%d].listen", i), f.Listen); err != nil {
			return err
		}
		if err := validHostPort(fmt.Sprintf("listen_forwards[%d].target", i), f.Target); err != nil {
			return err
		}
	}
	return nil
}

// validHostPort requires a concrete host:port with a numeric port and no
// wildcards in the host.
func validHostPort(field, addr string) error {
	if addr == "" {
		return fmt.Errorf("config: %s is required", field)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("config: %s %q must be host:port: %w", field, addr, err)
	}
	if host == "" || strings.Contains(host, "*") {
		return fmt.Errorf("config: %s %q needs a concrete host", field, addr)
	}
	p, err := strconv.Atoi(port)
	if err != nil || p <= 0 || p > 65535 {
		return fmt.Errorf("config: %s %q has an invalid port", field, addr)
	}
	return nil
}

func validPort(field string, port int) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("config: %s %d is not a valid port", field, port)
	}
	return nil
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
