package proxy

import (
	"encoding/hex"
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

	"dproxy/internal/httpsniff"
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
	ControlSocket string `yaml:"control_socket"`

	HTTP    *HTTPConfig    `yaml:"http"`
	HTTPS   *HTTPSConfig   `yaml:"https"`
	TCP     []TCPRoute     `yaml:"tcp"`
	SSH     *SSHConfig     `yaml:"ssh"`
	RDP     *RDPConfig     `yaml:"rdp"`
	Auth    *AuthConfig    `yaml:"auth"`
	Console *ConsoleConfig `yaml:"console"`
	Desktop *DesktopConfig `yaml:"desktop"`
	Site    *SiteConfig    `yaml:"site"`
	ACME    *ACMEConfig    `yaml:"acme"`

	ListenForwards []ForwardConfig `yaml:"listen_forwards"`

	DialTimeout       Duration `yaml:"dial_timeout"`
	HTTPSniffTimeout  Duration `yaml:"http_sniff_timeout"`
	HTTPSniffMaxBytes int      `yaml:"http_sniff_max_bytes"`
	LogLevel          string   `yaml:"log_level"`
}

type HTTPConfig struct {
	Listen    string              `yaml:"listen"`
	Reuseport bool                `yaml:"reuseport"`
	Hosts     map[string]HTTPHost `yaml:"hosts"`
	Default   string              `yaml:"default"`
}

const portSeparator = "--"

type HTTPHost struct {
	Host                 string `yaml:"host"`
	UnauthenticatedPorts []int  `yaml:"unauthenticated_ports"`
	DefaultPort          int    `yaml:"default_port"`
}

func (h HTTPHost) Target() string {
	return net.JoinHostPort(h.Host, strconv.Itoa(h.DefaultPort))
}

func (h HTTPHost) NeedsAuth(port int) bool {
	return !slices.Contains(h.UnauthenticatedPorts, port)
}

type AuthConfig struct {
	ControlURL       string   `yaml:"control_url"`
	CookieName       string   `yaml:"cookie_name"`
	CookieSecretFile string   `yaml:"cookie_secret_file"`
	CookieTTL        Duration `yaml:"cookie_ttl"`
	CookieSecure     bool     `yaml:"cookie_secure"`
	CookieSameSite string `yaml:"cookie_samesite"`
}

type ConsoleConfig struct {
	Label string `yaml:"label"`
	RemoteUser string `yaml:"remote_user"`
}

// DesktopConfig is the browser remote desktop, the console's counterpart: the
// same labelled-hostname entry point, landing on the guest's RDP port instead of
// its ssh port. The in-browser client speaks RDP itself, so unlike the console
// the guest's own credentials travel to it.
type DesktopConfig struct {
	Label          string `yaml:"label"`
	RemoteUser     string `yaml:"remote_user"`
	RemotePassword string `yaml:"remote_password"`
}

const defaultDesktopLabel = "desk"

func (c *DesktopConfig) label() string {
	if c.Label != "" {
		return c.Label
	}
	return defaultDesktopLabel
}

func (c *DesktopConfig) remoteUser() string {
	if c.RemoteUser != "" {
		return c.RemoteUser
	}
	return defaultConsoleRemoteUser
}

const defaultConsoleLabel = "shell"

var hostLabelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

var hostnamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

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

type SiteConfig struct {
	Listen string `yaml:"listen"`
	Hosts []string `yaml:"hosts"`
	HTMLFile string `yaml:"html_file"`
}

const defaultSiteListen = "127.0.0.1:8079"

func (s *SiteConfig) listen() string {
	if s.Listen != "" {
		return s.Listen
	}
	return defaultSiteListen
}

func (s *SiteConfig) entry() (HTTPHost, error) {
	host, port, err := net.SplitHostPort(s.listen())
	if err != nil {
		return HTTPHost{}, err
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		return HTTPHost{}, err
	}
	return HTTPHost{Host: host, UnauthenticatedPorts: []int{p}, DefaultPort: p}, nil
}

// ACMEConfig sends one path on the plain-http ingress somewhere other than the
// VM the Host header names: the certificate authority checks a name before any
// certificate for it exists, so the request cannot be answered over TLS and
// cannot be answered by the guest.
type ACMEConfig struct {
	ChallengeTarget string `yaml:"challenge_target"`
}

const ACMEChallengePrefix = "/.well-known/acme-challenge/"

type HTTPSConfig struct {
	Listen    string `yaml:"listen"`
	Reuseport bool   `yaml:"reuseport"`
}

type TCPRoute struct {
	Listen    string `yaml:"listen"`
	Target    string `yaml:"target"`
	Protocol  string `yaml:"protocol"`
	Reuseport bool   `yaml:"reuseport"`
}

type SSHConfig struct {
	Listen    string    `yaml:"listen"`
	Reuseport bool      `yaml:"reuseport"`
	Users     []SSHUser `yaml:"users"`
}

type SSHUser struct {
	PubKey     string `yaml:"pubkey"`
	PubKeyFile string `yaml:"pubkey_file"`
	VMName     string `yaml:"vm_name"`
	Target     string `yaml:"target"`
	RemoteUser string `yaml:"remote_user"`
}

// RDPConfig is the native remote-desktop ingress. It mirrors SSHConfig: one
// shared listener, and the login name the client authenticates with selects the
// VM. The difference is that the credential is issued by the control server
// rather than owned by the user, so the policy carries a hash instead of a key.
type RDPConfig struct {
	Listen    string    `yaml:"listen"`
	Reuseport bool      `yaml:"reuseport"`
	Users     []RDPUser `yaml:"users"`
}

type RDPUser struct {
	VMName string `yaml:"vm_name"`
	// NTHash is hex MD4(UTF16-LE(password)) — enough to verify an NTLMv2
	// response, and never the password itself.
	NTHash string `yaml:"nt_hash"`
	Target string `yaml:"target"`
	// RemoteUser and RemotePassword are the guest's own credentials, which dpipe
	// presents on the backend leg after the client has been authorized.
	RemoteUser     string `yaml:"remote_user"`
	RemotePassword string `yaml:"remote_password"`
}

type ForwardConfig struct {
	Listen string `yaml:"listen"`
	Target string `yaml:"target"`
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
		if c.HTTP == nil || (len(c.HTTP.Hosts) == 0 && c.Site == nil) {
			return errors.New("config: https requires http.hosts or site (HTTPS routing reuses the host map)")
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
			if u.VMName == "" || strings.ContainsAny(u.VMName, " \t@:") {
				return fmt.Errorf("config: ssh.users[%d].vm_name %q is not a usable ssh login name", i, u.VMName)
			}
			if err := validHostPort(fmt.Sprintf("ssh.users[%d].target", i), u.Target); err != nil {
				return err
			}
		}
	}
	if c.RDP != nil {
		if err := claim("rdp", c.RDP.Listen); err != nil {
			return err
		}
		ingress++
		if len(c.RDP.Users) == 0 {
			return errors.New("config: rdp requires at least one user")
		}
		names := map[string]bool{}
		for i, u := range c.RDP.Users {
			if u.VMName == "" || strings.ContainsAny(u.VMName, " \t@:\\") {
				return fmt.Errorf("config: rdp.users[%d].vm_name %q is not a usable login name", i, u.VMName)
			}
			// The login name is the routing key, so a duplicate would silently
			// make one of the two VMs unreachable.
			lower := strings.ToLower(u.VMName)
			if names[lower] {
				return fmt.Errorf("config: rdp.users[%d].vm_name %q is listed twice", i, u.VMName)
			}
			names[lower] = true
			if _, err := hex.DecodeString(u.NTHash); err != nil || len(u.NTHash) != 32 {
				return fmt.Errorf("config: rdp.users[%d].nt_hash must be 32 hex characters", i)
			}
			if u.RemoteUser == "" {
				return fmt.Errorf("config: rdp.users[%d].remote_user is required", i)
			}
			if err := validHostPort(fmt.Sprintf("rdp.users[%d].target", i), u.Target); err != nil {
				return err
			}
		}
	}
	if ingress == 0 {
		return errors.New("config: at least one ingress (http, https, tcp, ssh or rdp) is required")
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
			if !c.Auth.CookieSecure {
				return errors.New("config: auth.cookie_samesite: none requires auth.cookie_secure: true (browsers drop such a cookie over plaintext)")
			}
		default:
			return fmt.Errorf("config: auth.cookie_samesite %q must be \"lax\" or \"none\"", c.Auth.CookieSameSite)
		}
	}

	if c.Console != nil {
		if c.Auth == nil {
			return errors.New("config: console requires auth (the console token is verified with auth.cookie_secret_file)")
		}
		if c.HTTP == nil {
			return errors.New("config: console requires http.hosts (a console host is derived from a VM's host entry)")
		}
		if l := c.Console.label(); !hostLabelPattern.MatchString(l) {
			return fmt.Errorf("config: console.label %q is not a single hostname label", l)
		}
	}

	if c.Desktop != nil {
		if c.Auth == nil {
			return errors.New("config: desktop requires auth (the desktop token is verified with auth.cookie_secret_file)")
		}
		if c.HTTP == nil {
			return errors.New("config: desktop requires http.hosts (a desktop host is derived from a VM's host entry)")
		}
		if l := c.Desktop.label(); !hostLabelPattern.MatchString(l) {
			return fmt.Errorf("config: desktop.label %q is not a single hostname label", l)
		}
		if c.Console != nil && c.Desktop.label() == c.Console.label() {
			return fmt.Errorf("config: desktop.label and console.label are both %q", c.Desktop.label())
		}
		if c.Desktop.RemotePassword == "" {
			return errors.New("config: desktop.remote_password is required (the browser client authenticates to the guest itself)")
		}
	}

	if c.Site != nil {
		if err := claim("site", c.Site.listen()); err != nil {
			return err
		}
		if err := validHostPort("site.listen", c.Site.listen()); err != nil {
			return err
		}
		if c.HTTP == nil {
			return errors.New("config: site requires an http ingress (a site host is routed through the host map)")
		}
		if c.Site.HTMLFile == "" {
			return errors.New("config: site.html_file is required")
		}
		if len(c.Site.Hosts) == 0 {
			return errors.New("config: site requires at least one host")
		}
		for i, h := range c.Site.Hosts {
			if !hostnamePattern.MatchString(httpsniff.NormalizeHost(h)) {
				return fmt.Errorf("config: site.hosts[%d] %q is not a hostname", i, h)
			}
		}
	}

	if c.ACME != nil {
		if c.HTTP == nil {
			return errors.New("config: acme requires an http ingress (the challenge is answered over plain http)")
		}
		if err := validHostPort("acme.challenge_target", c.ACME.ChallengeTarget); err != nil {
			return err
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
