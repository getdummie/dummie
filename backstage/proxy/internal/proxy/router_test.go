package proxy

import "testing"

func TestHostBackend(t *testing.T) {
	cfg := &Config{HTTP: &HTTPConfig{Hosts: map[string]HTTPHost{
		"ONE.vm.local": {Host: "127.0.0.1", UnauthenticatedPorts: []int{8001}, DefaultPort: 8001},
		"two.vm.local": {Host: "127.0.0.1", DefaultPort: 8002},
	}}}
	r := NewRouter(cfg)

	cases := []struct {
		host   string
		want   string
		wantOK bool
	}{
		{"one.vm.local", "127.0.0.1:8001", true},
		{"ONE.VM.LOCAL", "127.0.0.1:8001", true},
		{"one.vm.local:8443", "127.0.0.1:8001", true},
		{"two.vm.local", "127.0.0.1:8002", true},
		{"nope.vm.local", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := r.HostBackend(c.host)
		if got != c.want || ok != c.wantOK {
			t.Errorf("HostBackend(%q) = (%q, %v), want (%q, %v)", c.host, got, ok, c.want, c.wantOK)
		}
	}
}

func TestHostBackendDefault(t *testing.T) {
	cfg := &Config{HTTP: &HTTPConfig{
		Hosts:   map[string]HTTPHost{"one.vm.local": {Host: "127.0.0.1", DefaultPort: 8001}},
		Default: "127.0.0.1:9999",
	}}
	r := NewRouter(cfg)
	if got, ok := r.HostBackend("unknown.local"); !ok || got != "127.0.0.1:9999" {
		t.Fatalf("default route = (%q, %v)", got, ok)
	}
}

func TestTCPRoutes(t *testing.T) {
	cfg := &Config{TCP: []TCPRoute{{Listen: ":9000", Target: "127.0.0.1:9000"}}}
	r := NewRouter(cfg)
	routes := r.TCPRoutes()
	if len(routes) != 1 || routes[0].Target != "127.0.0.1:9000" {
		t.Fatalf("routes = %+v", routes)
	}
}
