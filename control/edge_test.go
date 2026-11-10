package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"control/internal/db"
)

func TestGenerateEdgeProxyConfig(t *testing.T) {
	out := generateEdgeProxyConfig(edgeRoutes{
		hosts: [][2]string{
			{"build.vm.local", "10.68.0.2"},
			{"app.example.com", "10.68.0.3"},
			{"Bad Name", "10.68.0.3"},
		},
		tlds:     []string{"vm.local"},
		fallback: map[string][]string{"vm.local": {"10.68.0.2", "10.68.0.3"}},
		ssh: []db.ListEdgeSSHUsersRow{
			{VMName: "build", PublicKey: "ssh-ed25519 AAAAC3Nz one", Address: "10.68.0.2"},
		},
	})

	var got struct {
		HTTP struct {
			Hosts map[string]any `yaml:"hosts"`
		} `yaml:"http"`
		HTTPS map[string]any `yaml:"https"`
		Edge  struct {
			Hosts    map[string]string   `yaml:"hosts"`
			Fallback map[string][]string `yaml:"fallback"`
		} `yaml:"edge"`
		SSH struct {
			Users []struct {
				VMName     string `yaml:"vm_name"`
				Target     string `yaml:"target"`
				RemoteUser string `yaml:"remote_user"`
			} `yaml:"users"`
		} `yaml:"ssh"`
		Auth map[string]any `yaml:"auth"`
	}
	if err := yaml.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not valid yaml: %v\n%s", err, out)
	}
	if len(got.HTTP.Hosts) != 0 || got.HTTPS == nil || got.Auth != nil {
		t.Errorf("an edge forwards and authenticates nothing itself:\n%s", out)
	}
	if got.Edge.Hosts["build.vm.local"] != "10.68.0.2" || got.Edge.Hosts["app.example.com"] != "10.68.0.3" {
		t.Errorf("edge.hosts = %v", got.Edge.Hosts)
	}
	if len(got.Edge.Hosts) != 2 {
		t.Errorf("an unusable hostname should be skipped: %v", got.Edge.Hosts)
	}
	if fb := got.Edge.Fallback["vm.local"]; len(fb) != 2 || fb[0] != "10.68.0.2" {
		t.Errorf("edge.fallback = %v", got.Edge.Fallback)
	}
	if len(got.SSH.Users) != 1 {
		t.Fatalf("ssh.users = %+v", got.SSH.Users)
	}
	u := got.SSH.Users[0]
	if u.Target != "10.68.0.2:22" || u.RemoteUser != "build" {
		t.Errorf("the edge should log in to the host as the vm's name: %+v", u)
	}
}

func TestGenerateProxyConfigTrustsTheEdge(t *testing.T) {
	rows := []db.ListProxySSHUsersByClientRow{
		{VMIP: "10.64.0.2", HostVMID: "abc123", VMName: "build", PublicKey: "ssh-ed25519 AAAAC3Nz one"},
	}
	host := proxyHost{upstream: &edgeUpstream{pubkey: "ssh-ed25519 AAAAEDGE dpipe-edge", source: "10.68.0.11"}}
	out := generateProxyConfig(testProxyAuth, host, rows, nil, nil, nil)

	var got struct {
		SSH struct {
			TrustedUpstreams []struct {
				PubKey  string   `yaml:"pubkey"`
				Sources []string `yaml:"sources"`
			} `yaml:"trusted_upstreams"`
		} `yaml:"ssh"`
	}
	if err := yaml.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not valid yaml: %v\n%s", err, out)
	}
	if len(got.SSH.TrustedUpstreams) != 1 {
		t.Fatalf("trusted_upstreams = %+v\n%s", got.SSH.TrustedUpstreams, out)
	}
	tu := got.SSH.TrustedUpstreams[0]
	if tu.PubKey != "ssh-ed25519 AAAAEDGE dpipe-edge" || len(tu.Sources) != 1 || tu.Sources[0] != "10.68.0.11" {
		t.Errorf("trusted upstream = %+v", tu)
	}

	if strings.Contains(generateProxyConfig(testProxyAuth, proxyHost{}, rows, nil, nil, nil), "trusted_upstreams") {
		t.Error("a host without an edge should trust no upstream")
	}
}

func TestValidateAdvertiseIP(t *testing.T) {
	for in, want := range map[string]string{"": "", " 10.68.0.2 ": "10.68.0.2", "fd00::1": "fd00::1"} {
		if got, err := validateAdvertiseIP(in); err != nil || got != want {
			t.Errorf("validateAdvertiseIP(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := validateAdvertiseIP("host.example.com"); err == nil {
		t.Error("a hostname is not an address")
	}
}
