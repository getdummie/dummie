package main

import (
	"strings"
	"testing"
)

func TestGenerateIntproxyConfig(t *testing.T) {
	out := generateIntproxyConfig("example.com", "https://control.example.com/")

	for _, want := range []string{
		`listen: "10.64.255.254:443"`,
		`tld: "example.com"`,
		`label: "int"`,
		`console_url: "https://control.example.com"`,
		`cert: "/etc/intproxy/certs/fullchain.pem"`,
		`key: "/etc/intproxy/certs/privkey.pem"`,
		"socket: /run/intproxy/broker.sock",
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
	out := generateIntproxyConfig("example.com", "https://control.example.com")

	for _, forbidden := range []string{"10.64.0.", "integration:", "repos:", "installation"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("the config leaks policy (%q):\n%s", forbidden, out)
		}
	}
}

// dproxy holds 0.0.0.0:443 on the same host; a wildcard here would collide with
// it rather than coexist.
func TestGenerateIntproxyConfigNeverBindsAWildcard(t *testing.T) {
	out := generateIntproxyConfig("example.com", "https://control.example.com")

	if strings.Contains(out, "0.0.0.0") {
		t.Errorf("the listen address is a wildcard:\n%s", out)
	}
	if !strings.Contains(out, "reuseport: true") {
		t.Errorf("reuseport is off, so the bind will fail against dproxy:\n%s", out)
	}
}

// A packfile clone is one long request in each direction, so a total deadline
// would cut it off mid-transfer.
func TestGenerateIntproxyConfigSetsNoBodyDeadline(t *testing.T) {
	out := generateIntproxyConfig("example.com", "https://control.example.com")

	for _, forbidden := range []string{"\nread_timeout:", "\nwrite_timeout:"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("a body deadline was written (%q):\n%s", strings.TrimSpace(forbidden), out)
		}
	}
}
