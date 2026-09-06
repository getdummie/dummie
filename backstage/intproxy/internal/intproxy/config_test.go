package intproxy

import (
	"strings"
	"testing"
)

func TestValidateRejectsWildcardListen(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:443", ":443", "[::]:443"} {
		c := testConfig()
		c.Listen = addr
		err := c.Validate()
		if err == nil {
			t.Fatalf("listen %q was accepted; it would collide with dproxy", addr)
		}
		if !strings.Contains(err.Error(), "wildcard") {
			t.Fatalf("listen %q: %v", addr, err)
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
		{"no socket", func(c *Config) { c.Broker.Socket = "" }, "broker.socket is required"},
		{"relative socket", func(c *Config) { c.Broker.Socket = "broker.sock" }, "absolute path"},
		{"hostname listen", func(c *Config) { c.Listen = "intproxy:443" }, "needs an IP address"},
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

func TestDefaults(t *testing.T) {
	c := &Config{}
	if c.label() != "int" {
		t.Fatalf("label default = %q", c.label())
	}
	if c.GitHub.gitHost() != "github.com" || c.GitHub.apiHost() != "api.github.com" {
		t.Fatalf("github defaults = %s / %s", c.GitHub.gitHost(), c.GitHub.apiHost())
	}
}
