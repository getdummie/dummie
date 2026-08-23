package proxy

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"regexp"
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

	HTTP    *HTTPConfig    `yaml:"http"`
	HTTPS   *HTTPSConfig   `yaml:"https"`
	TCP     []TCPRoute     `yaml:"tcp"`
	SSH     *SSHConfig     `yaml:"ssh"`
	Auth    *AuthConfig    `yaml:"auth"`
	Console *ConsoleConfig `yaml:"console"`

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

// portSeparator marks an explicit port in a hostname: one--9922.vm.local routes
// to port 9922 on the host configured as one.vm.local.
const portSeparator = "--"

// HTTPHost is one routed hostname: the backend machine, the port requests are
// routed to, and which of its ports may be reached without authenticating.
type HTTPHost struct {
	Host                 string `yaml:"host"`
	UnauthenticatedPorts []int  `yaml:"unauthenticated_ports"`
	DefaultPort          int    `yaml:"default_port"`
}

// Target is the backend address requests for this hostname are sent to.
func (h HTTPHost) Target() string {
	return net.JoinHostPort(h.Host, strconv.Itoa(h.DefaultPort))
}

// NeedsAuth reports whether reaching port on this host requires an
// authenticated user. Ports absent from unauthenticated_ports are protected, so
// an empty or omitted list protects everything.
func (h HTTPHost) NeedsAuth(port int) bool {
	return !slices.Contains(h.UnauthenticatedPorts, port)
}

// AuthConfig points at the control server that authenticates users and holds
// the secret it shares with the proxy. Required once any host protects a port.
type AuthConfig struct {
	ControlURL       string   `yaml:"control_url"`
	CookieName       string   `yaml:"cookie_name"`
	CookieSecretFile string   `yaml:"cookie_secret_file"`
	CookieTTL        Duration `yaml:"cookie_ttl"`
	CookieSecure     bool     `yaml:"cookie_secure"`
	// CookieSameSite decides whether a guest can be reached from a page on
	// another site -- which is what the control server's work view does when it
	// frames a guest, since the control plane and the guests are deliberately on
	// different domains.
	//
	// "lax" (the default) means the browser sends the cookie only when the guest
	// and the top-level page are the same site, so a framed guest gets no cookie
	// and every request looks unauthenticated. "none" lifts that, and the browser
	// then requires Secure, so it is only usable where guests are served over
	// https. Partitioned is set with it: the cookie a framed guest gets is keyed
	// to the page framing it, so it is not the same session as a direct visit and
	// cannot be reached from anywhere else that embeds the same guest.
	CookieSameSite string `yaml:"cookie_samesite"`
}

// ConsoleConfig enables the browser terminal. Its presence is what claims the
// console hostnames: with no console: block those names route nowhere, so the
// endpoint cannot be reached on a host that was not meant to offer it.
type ConsoleConfig struct {
	// Label is the extra hostname label that marks a console host, so
	// "<vm>.console.<domain>" is the terminal for "<vm>.<domain>". Configurable
	// only because it also has to match a DNS record and a certificate, which are
	// cut outside this file.
	Label string `yaml:"label"`
	// RemoteUser is the account the shell runs as inside the guest.
	RemoteUser string `yaml:"remote_user"`
}

const defaultConsoleLabel = "console"

// hostLabelPattern is one DNS label. The label is spliced out of a hostname to
// find the VM behind a console name, so a value containing a dot would make one
// console name mean two different things.
var hostLabelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func (c *ConsoleConfig) label() string {
	if c.Label != "" {
		return c.Label
	}
	return defaultConsoleLabel
}

func (c *ConsoleConfig) remoteUser() string {
	if c.RemoteUser != "" {
		return c.RemoteUser
	}
	return defaultConsoleRemoteUser
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
			// "--" is reserved for the name--port form, which is resolved
			// against the base hostname's entry. A literal key containing it
			// would be a second, ambiguous source for the same request.
			if strings.Contains(host, portSeparator) {
				return fmt.Errorf("config: http.hosts key %q must not contain %q (reserved for the name%sport form)",
					host, portSeparator, portSeparator)
			}
			field := "http.hosts[" + host + "]"
			if h.Host == "" || strings.Contains(h.Host, "*") {
				return fmt.Errorf("config: %s.host %q needs a concrete host", field, h.Host)
			}
			if err := validPort(field+".default_port", h.DefaultPort); err != nil {
				return err
			}
			for i, p := range h.UnauthenticatedPorts {
				if err := validPort(fmt.Sprintf("%s.unauthenticated_ports[%d]", field, i), p); err != nil {
					return err
				}
			}
			// Fail closed: a host that wants auth but has nowhere to send the
			// user would otherwise serve the site unauthenticated.
			if h.NeedsAuth(h.DefaultPort) && c.Auth == nil {
				return fmt.Errorf("config: %s requires auth (port %d is not in unauthenticated_ports) but auth: is not configured",
					field, h.DefaultPort)
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

	if c.Auth != nil {
		if c.Auth.ControlURL == "" {
			return errors.New("config: auth.control_url is required")
		}
		if _, err := url.Parse(c.Auth.ControlURL); err != nil {
			return fmt.Errorf("config: auth.control_url %q: %w", c.Auth.ControlURL, err)
		}
		if c.Auth.CookieSecretFile == "" {
			return errors.New("config: auth.cookie_secret_file is required")
		}
		switch strings.ToLower(strings.TrimSpace(c.Auth.CookieSameSite)) {
		case "", "lax":
		case "none":
			// Rejected rather than corrected: a browser silently drops a
			// SameSite=None cookie that is not Secure, and the failure that
			// follows looks like "login did nothing" on every request.
			if !c.Auth.CookieSecure {
				return errors.New("config: auth.cookie_samesite: none requires auth.cookie_secure: true (browsers drop such a cookie over plaintext)")
			}
		default:
			return fmt.Errorf("config: auth.cookie_samesite %q must be \"lax\" or \"none\"", c.Auth.CookieSameSite)
		}
	}

	if c.Console != nil {
		// The console is a shell. A signed token is the only thing between the
		// internet and it, and without auth: there is no key to verify one with.
		if c.Auth == nil {
			return errors.New("config: console requires auth (the console token is verified with auth.cookie_secret_file)")
		}
		// Console hostnames are derived from the http host table, so without one
		// there is nothing a console name could resolve to.
		if c.HTTP == nil {
			return errors.New("config: console requires http.hosts (a console host is derived from a VM's host entry)")
		}
		if l := c.Console.label(); !hostLabelPattern.MatchString(l) {
			return fmt.Errorf("config: console.label %q is not a single hostname label", l)
		}
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
