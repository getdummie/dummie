package main

import (
	"net"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type parsedIntproxyConfig struct {
	Listen    string `yaml:"listen"`
	Reuseport bool   `yaml:"reuseport"`
	TLS       struct {
		Enabled *bool  `yaml:"enabled"`
		Cert    string `yaml:"cert"`
		Key     string `yaml:"key"`
	} `yaml:"tls"`
}

func parseIntproxyConfig(t *testing.T, out string) parsedIntproxyConfig {
	t.Helper()
	var got parsedIntproxyConfig
	if err := yaml.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid intproxy config: %v\n%s", err, out)
	}
	return got
}

func TestGenerateIntproxyConfig(t *testing.T) {
	out := generateIntproxyConfig("example.com", "https://control.example.com/", true)

	for _, want := range []string{
		`listen: "10.64.255.254:443"`,
		`tld: "example.com"`,
		`label: "int"`,
		`docs_url: "https://control.example.com/integrations"`,
		`cert: "/etc/intproxy/certs/fullchain.pem"`,
		`key: "/etc/intproxy/certs/privkey.pem"`,
		"socket: /run/intproxy/broker.sock",
		"mode: broker",
		"- name: github",
		"- name: llm",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

// The config carries no policy at all. A vm-to-integration map here would be a
// second copy of the authorization rules, and a stale one would keep a detached
// vm working.
func TestGenerateIntproxyConfigCarriesNoPolicy(t *testing.T) {
	out := generateIntproxyConfig("example.com", "https://control.example.com", true)

	for _, forbidden := range []string{"10.64.0.", "integration:", "repos:", "installation"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("the config leaks policy (%q):\n%s", forbidden, out)
		}
	}
}

// dproxy holds 0.0.0.0:443 on the same host; a wildcard here would collide with
// it rather than coexist.
func TestGenerateIntproxyConfigNeverBindsAWildcard(t *testing.T) {
	out := generateIntproxyConfig("example.com", "https://control.example.com", true)
	got := parseIntproxyConfig(t, out)

	host, _, err := net.SplitHostPort(got.Listen)
	if err != nil {
		t.Fatalf("invalid listen address %q: %v", got.Listen, err)
	}
	if ip := net.ParseIP(host); ip == nil || ip.IsUnspecified() {
		t.Errorf("the listen address is not a concrete IP: %q", got.Listen)
	}
	if !got.Reuseport {
		t.Errorf("reuseport is off, so the bind will fail against dproxy:\n%s", out)
	}
}

// A packfile clone is one long request in each direction, so a total deadline
// would cut it off mid-transfer.
func TestGenerateIntproxyConfigSetsNoBodyDeadline(t *testing.T) {
	out := generateIntproxyConfig("example.com", "https://control.example.com", true)

	for _, forbidden := range []string{"\nread_timeout:", "\nwrite_timeout:"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("a body deadline was written (%q):\n%s", strings.TrimSpace(forbidden), out)
		}
	}
}

// A fleet with no wildcard still gets a usable proxy, over http on :80. The
// credential is added on the upstream leg either way, so it is not exposed.
func TestGenerateIntproxyConfigWithoutTLS(t *testing.T) {
	out := generateIntproxyConfig("example.com", "https://control.example.com", false)
	got := parseIntproxyConfig(t, out)

	if got.Listen != "10.64.255.254:80" {
		t.Errorf("expected the plain listener:\n%s", out)
	}
	if got.TLS.Enabled == nil || *got.TLS.Enabled {
		t.Errorf("tls was not turned off:\n%s", out)
	}
	if got.TLS.Cert != "" || got.TLS.Key != "" {
		t.Errorf("the tls-off config still has certificate paths:\n%s", out)
	}
}
