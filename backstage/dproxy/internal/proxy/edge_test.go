package proxy

import (
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"dproxy/internal/control"
)

func TestEdgeBackends(t *testing.T) {
	r := newEdgeRouter(&EdgeConfig{
		Hosts: map[string]string{
			"one.vm.local":    "10.68.0.2",
			"two.vm.local":    "10.68.0.3",
			"app.example.com": "10.68.0.3",
		},
		Fallback: map[string][]string{"vm.local": {"10.68.0.2", "10.68.0.3"}},
	})
	cases := map[string][]string{
		"one.vm.local":        {"10.68.0.2"},
		"ONE.vm.local:443":    {"10.68.0.2"},
		"one--8080.vm.local":  {"10.68.0.2"},
		"two.shell.vm.local":  {"10.68.0.3"},
		"two.desk.vm.local":   {"10.68.0.3"},
		"app.example.com":     {"10.68.0.3"},
		"www.vm.local":        {"10.68.0.2", "10.68.0.3"},
		"missing.vm.local":    {"10.68.0.2", "10.68.0.3"},
		"vm.local":            {"10.68.0.2", "10.68.0.3"},
		"other.example.com":   nil,
		"":                    nil,
		"one.vm.local.evil.io": nil,
	}
	for host, want := range cases {
		if got := r.backends(host); !slices.Equal(got, want) {
			t.Errorf("backends(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestEdgeConfigRejectsServing(t *testing.T) {
	base := func() *Config {
		return &Config{
			ControlSocket: "/tmp/x.sock",
			HTTP:          &HTTPConfig{Listen: ":80"},
			HTTPS:         &HTTPSConfig{Listen: ":443"},
			Edge:          &EdgeConfig{Hosts: map[string]string{"one.vm.local": "10.68.0.2"}},
		}
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("plain edge config: %v", err)
	}

	withHosts := base()
	withHosts.HTTP.Hosts = map[string]HTTPHost{"one.vm.local": {Host: "10.64.0.2", DefaultPort: 80, UnauthenticatedPorts: []int{80}}}
	if err := withHosts.Validate(); err == nil || !strings.Contains(err.Error(), "http.hosts") {
		t.Fatalf("edge with http.hosts: err = %v", err)
	}

	withPort := base()
	withPort.Edge.Hosts["one.vm.local"] = "10.68.0.2:80"
	if err := withPort.Validate(); err == nil {
		t.Fatal("edge.hosts with a port should be rejected")
	}
}

func TestResolveSSHTrustedUpstream(t *testing.T) {
	alice, edge := newTestKey(t), newTestKey(t)
	cfg := &Config{
		ControlSocket: "/tmp/ignored.sock",
		SSH: &SSHConfig{
			Listen: ":2222",
			Users: []SSHUser{{
				PubKey: authorizedLine(alice, ""), VMName: "build",
				Target: "10.64.0.5:22", RemoteUser: "dev",
			}},
			TrustedUpstreams: []TrustedUpstream{{PubKey: authorizedLine(edge, " edge"), Sources: []string{"10.68.0.11"}}},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	r := newTestResolver(t, cfg)
	ask := func(key ssh.PublicKey, vm, ip string) control.Msg {
		return r.Handle(control.Msg{
			V: control.Version, Type: control.TypeResolve, ID: "r", Kind: control.KindSSH,
			SSHUser: vm, SSHPubKey: authorizedLine(key, ""), ClientIP: ip,
		})
	}

	rep := ask(edge, "build", "10.68.0.11")
	if !rep.Authorized || rep.Target != "10.64.0.5:22" || rep.RemoteUser != "dev" {
		t.Fatalf("edge from its source: %+v", rep)
	}
	if rep := ask(edge, "build", "10.68.0.12"); rep.Authorized {
		t.Fatalf("edge key from another address must be denied: %+v", rep)
	}
	if rep := ask(edge, "nope", "10.68.0.11"); rep.Authorized || rep.Notice != "" {
		t.Fatalf("edge asking for an unknown vm: %+v", rep)
	}
	if rep := ask(alice, "build", "10.68.0.11"); !rep.Authorized {
		t.Fatalf("a user's own key still works directly: %+v", rep)
	}
}
