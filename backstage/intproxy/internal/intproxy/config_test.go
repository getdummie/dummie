package intproxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"intproxy/internal/credential"
	_ "intproxy/internal/integration/all"
	"intproxy/internal/policy"
)

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "intproxy.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func testConfig() *Config {
	return &Config{
		Listen:    "10.64.255.254:443",
		Freebind:  true,
		Reuseport: true,
		TLD:       "example.com",
		Label:     "int",
		DocsURL:   "https://control.example.com/integrations",
		TLS:       TLSConfig{Cert: "cert.pem", Key: "key.pem"},
		Credential: CredentialConfig{
			Mode:   ModeBroker,
			Broker: BrokerConfig{Socket: "/run/intproxy/broker.sock"},
		},
		Integrations: []IntegrationConfig{{Name: "github", Enabled: true}},
	}
}

func localConfig() *Config {
	c := testConfig()
	c.Credential = CredentialConfig{Mode: ModeLocal}
	c.Integrations[0].Auth.Env = "INTPROXY_TEST_PAT"
	c.Policy = &policy.Policy{
		Clients: []policy.Client{{
			Name:         "build",
			Source:       "10.64.0.5",
			Integrations: []policy.ClientIntegration{{Name: "github", Read: []string{"acme/*"}}},
		}},
	}
	return c
}

// reuseport means another process holds this port and the two coexist only
// because this bind is the more specific one.
func TestValidateRejectsWildcardListenUnderReuseport(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:443", ":443", "[::]:443"} {
		c := testConfig()
		c.Listen = addr
		err := c.Validate()
		if err == nil {
			t.Fatalf("listen %q was accepted with reuseport on", addr)
		}
		if !strings.Contains(err.Error(), "wildcard") {
			t.Fatalf("listen %q: %v", addr, err)
		}
	}
}

// Standing alone there is nothing to share the port with, so a wildcard is
// perfectly ordinary.
func TestValidateAllowsWildcardListenWithoutReuseport(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:443", ":443", "intproxy.internal:443"} {
		c := localConfig()
		c.Reuseport = false
		c.Freebind = false
		c.Listen = addr
		if err := c.Validate(); err != nil {
			t.Fatalf("listen %q was rejected standing alone: %v", addr, err)
		}
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{"ok", func(*Config) {}, ""},
		{"no tld", func(c *Config) { c.TLD = "" }, "tld is required"},
		{"bad tld", func(c *Config) { c.TLD = "not a host" }, "is not a hostname"},
		{"bad label", func(c *Config) { c.Label = "a.b" }, "single hostname label"},
		{"no cert", func(c *Config) { c.TLS.Cert = "" }, "tls.cert and tls.key"},
		{"bad tls version", func(c *Config) { c.TLS.MinVersion = "1.1" }, "must be 1.2 or 1.3"},
		{"no mode", func(c *Config) { c.Credential.Mode = "" }, "credential.mode is required"},
		{"bad mode", func(c *Config) { c.Credential.Mode = "magic" }, "must be"},
		{"no socket", func(c *Config) { c.Credential.Broker.Socket = "" }, "credential.broker.socket is required"},
		{"relative socket", func(c *Config) { c.Credential.Broker.Socket = "broker.sock" }, "absolute path"},
		{"no integrations", func(c *Config) { c.Integrations = nil }, "at least one integration"},
		{"unknown integration", func(c *Config) { c.Integrations[0].Name = "llm" }, "is not known"},
		{"all disabled", func(c *Config) { c.Integrations[0].Enabled = false }, "every integration is disabled"},
		{"duplicate integration", func(c *Config) {
			c.Integrations = append(c.Integrations, IntegrationConfig{Name: "github", Enabled: true})
		}, "listed twice"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := testConfig()
			tc.mut(c)
			err := c.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// Under broker mode the control server answers on every request. A local copy
// of the rules, or of a credential, would be a second and staler answer.
func TestBrokerModeRefusesLocalPolicyAndAuth(t *testing.T) {
	c := testConfig()
	c.Policy = &policy.Policy{Clients: []policy.Client{{Source: "10.0.0.1"}}}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "policy belongs to mode: local") {
		t.Fatalf("a policy under broker mode was accepted: %v", err)
	}

	c = testConfig()
	c.Integrations[0].Auth.File = "/etc/intproxy/pat"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "holds no credentials") {
		t.Fatalf("auth under broker mode was accepted: %v", err)
	}
}

func TestLocalModeNeedsPolicyAndAuth(t *testing.T) {
	c := localConfig()
	c.Policy = nil
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "policy is required") {
		t.Fatalf("local mode without a policy was accepted: %v", err)
	}

	c = localConfig()
	c.Integrations[0].Auth = credential.StaticConfig{}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "needs an auth block") {
		t.Fatalf("local mode without auth was accepted: %v", err)
	}

	c = localConfig()
	c.Credential.Broker.Socket = "/run/intproxy/broker.sock"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "the mode is local") {
		t.Fatalf("a broker socket under local mode was accepted: %v", err)
	}
}

// A missing tls block must not be read as "plaintext is fine" -- this proxy
// injects credentials, so the default has to be the closed one.
func TestTLSDefaultsOn(t *testing.T) {
	c := testConfig()
	c.TLS.Cert = ""
	c.TLS.Key = ""
	if err := c.Validate(); err == nil {
		t.Fatal("a config with no certificate and no explicit tls.enabled was accepted")
	}

	off := false
	c.TLS.Enabled = &off
	if err := c.Validate(); err != nil {
		t.Fatalf("tls.enabled: false still demanded a certificate: %v", err)
	}
}

func TestLoadConfigDecodesIntegrationOptions(t *testing.T) {
	path := writeTemp(t, `
listen: "0.0.0.0:8443"
tld: "example.com"
tls:
  enabled: false
credential:
  mode: broker
  broker:
    socket: /run/intproxy/broker.sock
integrations:
  - name: github
    git_host: ghe.example.com
    api_host: ghe.example.com/api/v3
`)

	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Integrations) != 1 || !c.Integrations[0].Enabled {
		t.Fatalf("integrations = %+v", c.Integrations)
	}

	var opts struct {
		GitHost string `yaml:"git_host"`
		APIHost string `yaml:"api_host"`
	}
	if err := c.Integrations[0].Decode(&opts); err != nil {
		t.Fatal(err)
	}
	if opts.GitHost != "ghe.example.com" || opts.APIHost != "ghe.example.com/api/v3" {
		t.Fatalf("the integration's own options were not passed through: %+v", opts)
	}
}

func TestDefaults(t *testing.T) {
	c := &Config{TLD: "example.com"}
	if c.label() != "int" {
		t.Fatalf("label default = %q", c.label())
	}
	if got := c.hostname("github"); got != "github.int.example.com" {
		t.Fatalf("hostname = %q", got)
	}
}
