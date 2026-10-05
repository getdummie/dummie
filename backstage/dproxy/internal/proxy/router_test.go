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

func TestSiteHostsRouteToTheSiteListener(t *testing.T) {
	cfg := &Config{
		HTTP: &HTTPConfig{Hosts: map[string]HTTPHost{"one.vm.local": {Host: "10.0.0.2", DefaultPort: 8001}}},
		Site: &SiteConfig{Listen: "127.0.0.1:8079", Hosts: []string{"WWW.vm.local"}, HTMLFile: "/dev/null"},
	}
	r := NewRouter(cfg)

	got, ok := r.HostBackend("www.vm.local")
	if !ok || got != "127.0.0.1:8079" {
		t.Fatalf("site route = (%q, %v), want (127.0.0.1:8079, true)", got, ok)
	}
	entry, ok := r.HostEntry("www.vm.local")
	if !ok || entry.NeedsAuth(entry.DefaultPort) {
		t.Fatalf("site entry = (%+v, %v), want an unauthenticated one", entry, ok)
	}
}

func TestSiteHostsNeverOverrideAVM(t *testing.T) {
	cfg := &Config{
		HTTP: &HTTPConfig{Hosts: map[string]HTTPHost{"www.vm.local": {Host: "10.0.0.2", DefaultPort: 8001}}},
		Site: &SiteConfig{Listen: "127.0.0.1:8079", Hosts: []string{"www.vm.local"}, HTMLFile: "/dev/null"},
	}
	if got, _ := NewRouter(cfg).HostBackend("www.vm.local"); got != "10.0.0.2:8001" {
		t.Fatalf("route = %q, want the vm at 10.0.0.2:8001", got)
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

func TestPortHosts(t *testing.T) {
	cfg := &Config{HTTP: &HTTPConfig{Hosts: map[string]HTTPHost{
		"one.vm.local":   {Host: "10.64.0.2", UnauthenticatedPorts: []int{9000}, DefaultPort: 8000, PortHosts: true},
		"two.vm.local":   {Host: "10.64.0.3", DefaultPort: 8000},
		"my-vm.vm.local": {Host: "10.64.0.4", DefaultPort: 8000, PortHosts: true},
	}}}
	r := NewRouter(cfg)

	cases := []struct {
		host   string
		want   string
		wantOK bool
	}{
		{"one--9000.vm.local", "10.64.0.2:9000", true},
		{"ONE--9000.vm.local:443", "10.64.0.2:9000", true},
		{"my-vm--65535.vm.local", "10.64.0.4:65535", true},
		{"two--9000.vm.local", "", false},
		{"one--0.vm.local", "", false},
		{"one--65536.vm.local", "", false},
		{"one--09000.vm.local", "", false},
		{"one--+9000.vm.local", "", false},
		{"one--abc.vm.local", "", false},
		{"one--.vm.local", "", false},
		{"--9000.vm.local", "", false},
		{"nope--9000.vm.local", "", false},
	}
	for _, c := range cases {
		got, ok := r.HostBackend(c.host)
		if got != c.want || ok != c.wantOK {
			t.Errorf("HostBackend(%q) = (%q, %v), want (%q, %v)", c.host, got, ok, c.want, c.wantOK)
		}
	}
}

func TestPortHostsAuthFollowsThePort(t *testing.T) {
	cfg := &Config{HTTP: &HTTPConfig{Hosts: map[string]HTTPHost{
		"one.vm.local": {Host: "10.64.0.2", UnauthenticatedPorts: []int{9000}, DefaultPort: 8000, PortHosts: true},
	}}}
	r := NewRouter(cfg)

	if e, ok := r.HostEntry("one--9000.vm.local"); !ok || e.NeedsAuth(e.DefaultPort) {
		t.Errorf("port 9000 is unauthenticated but its host entry = (%+v, %v)", e, ok)
	}
	if e, ok := r.HostEntry("one--9001.vm.local"); !ok || !e.NeedsAuth(e.DefaultPort) {
		t.Errorf("port 9001 needs auth but its host entry = (%+v, %v)", e, ok)
	}
	if _, ok := r.PublishedEntry("one--9000.vm.local"); ok {
		t.Error("PublishedEntry answered for the name--port form")
	}
}

func TestPortHostsAreNotConsoleNames(t *testing.T) {
	cfg := &Config{
		HTTP: &HTTPConfig{Hosts: map[string]HTTPHost{
			"one.vm.local": {Host: "10.64.0.2", DefaultPort: 8000, PortHosts: true},
		}},
		Console: &ConsoleConfig{},
	}
	p := &Proxy{cfg: cfg, router: NewRouter(cfg)}

	if got, ok := p.consoleVMHost("one--9000.shell.vm.local"); ok {
		t.Errorf("consoleVMHost accepted the name--port form, routing to %q", got)
	}
	if _, ok := p.router.HostBackend("one--9000.shell.vm.local"); ok {
		t.Error("the name--port form under the console label still routes somewhere")
	}
}
