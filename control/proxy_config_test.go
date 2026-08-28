package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"control/internal/db"
)

var testProxyAuth = proxyAuthConfig{
	secret:     testProxySecret,
	controlURL: "http://control.example.com:1323",
}

type parsedProxyConfig struct {
	Auth struct {
		ControlURL       string `yaml:"control_url"`
		CookieSecretFile string `yaml:"cookie_secret_file"`
		CookieTTL        string `yaml:"cookie_ttl"`
		CookieSecure     bool   `yaml:"cookie_secure"`
		CookieSameSite   string `yaml:"cookie_samesite"`
	} `yaml:"auth"`
	SSH struct {
		Listen string `yaml:"listen"`
		Users  []struct {
			Pubkey     string `yaml:"pubkey"`
			VMName     string `yaml:"vm_name"`
			Target     string `yaml:"target"`
			RemoteUser string `yaml:"remote_user"`
		} `yaml:"users"`
	} `yaml:"ssh"`
	HTTP struct {
		Listen string `yaml:"listen"`
		Hosts  map[string]struct {
			Host string `yaml:"host"`
			UnauthPorts []int `yaml:"unauthenticated_ports"`
			DefaultPort int   `yaml:"default_port"`
		} `yaml:"hosts"`
		Default string `yaml:"default"`
	} `yaml:"http"`
	HTTPS struct {
		Listen    string `yaml:"listen"`
		Reuseport bool   `yaml:"reuseport"`
	} `yaml:"https"`
	Console struct {
		Label      string `yaml:"label"`
		RemoteUser string `yaml:"remote_user"`
	} `yaml:"console"`
	Site struct {
		Listen   string   `yaml:"listen"`
		HTMLFile string   `yaml:"html_file"`
		Hosts    []string `yaml:"hosts"`
	} `yaml:"site"`
}

func parseProxyConfig(t *testing.T, out string) parsedProxyConfig {
	t.Helper()
	var got parsedProxyConfig
	if err := yaml.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("the generated config is not valid yaml (%v):\n%s", err, out)
	}
	return got
}

func TestGenerateProxyConfigOneVMPerUser(t *testing.T) {
	rows := []db.ListProxySSHUsersByClientRow{
		{VMIP: "10.64.0.2", HostVMID: "abc123", VMName: "build", PublicKey: "ssh-ed25519 AAAAC3Nz one"},
		{VMIP: "10.64.0.3", HostVMID: "def456", VMName: "test", PublicKey: "ssh-ed25519 AAAAC3Ny two"},
	}

	got := parseProxyConfig(t, generateProxyConfig(testProxyAuth, proxyHost{}, rows, nil))
	if got.SSH.Listen != proxySSHListen {
		t.Errorf("listen is %q, want %q", got.SSH.Listen, proxySSHListen)
	}
	if len(got.SSH.Users) != 2 {
		t.Fatalf("got %d users, want 2", len(got.SSH.Users))
	}
	for i, want := range []struct{ pubkey, vmName, target string }{
		{"ssh-ed25519 AAAAC3Nz one", "build", "10.64.0.2:22"},
		{"ssh-ed25519 AAAAC3Ny two", "test", "10.64.0.3:22"},
	} {
		u := got.SSH.Users[i]
		if u.Pubkey != want.pubkey {
			t.Errorf("user %d pubkey is %q, want %q", i, u.Pubkey, want.pubkey)
		}
		if u.VMName != want.vmName {
			t.Errorf("user %d vm_name is %q, want %q", i, u.VMName, want.vmName)
		}
		if u.Target != want.target {
			t.Errorf("user %d target is %q, want %q", i, u.Target, want.target)
		}
		if u.RemoteUser != proxyRemoteUser {
			t.Errorf("user %d remote_user is %q, want %q", i, u.RemoteUser, proxyRemoteUser)
		}
	}
}

func TestGenerateProxyConfigEmptyHost(t *testing.T) {
	out := generateProxyConfig(testProxyAuth, proxyHost{}, nil, nil)
	if strings.Contains(out, "ssh:") {
		t.Errorf("an empty host emitted an ssh block, which proxy will not start on:\n%s", out)
	}
	if !strings.Contains(out, "hosts: {}") {
		t.Errorf("an empty host did not emit an empty host table:\n%s", out)
	}
	got := parseProxyConfig(t, out)
	if got.SSH.Listen != "" || len(got.SSH.Users) != 0 {
		t.Errorf("got an ssh block (listen %q, %d users), want none at all",
			got.SSH.Listen, len(got.SSH.Users))
	}
	if len(got.HTTP.Hosts) != 0 {
		t.Errorf("got %d http hosts, want 0", len(got.HTTP.Hosts))
	}
}

func TestGenerateProxyConfigHTTPHosts(t *testing.T) {
	rows := []db.ListProxyHTTPRoutesByClientRow{
		{VMName: "interesting-hawking", VMIP: "10.64.0.2", HostVMID: "abc123", DomainTLD: "example.com", DefaultPort: 8000, PublicPorts: []int32{8000}},
		{VMName: "quirky-curie", VMIP: "10.64.0.3", HostVMID: "def456", DomainTLD: "example.com", DefaultPort: 3000, PublicPorts: []int32{3000, 9090}},
	}

	got := parseProxyConfig(t, generateProxyConfig(testProxyAuth, proxyHost{}, nil, rows))
	if got.HTTP.Listen != proxyHTTPListen {
		t.Errorf("http listen is %q, want %q", got.HTTP.Listen, proxyHTTPListen)
	}
	if len(got.HTTP.Hosts) != 2 {
		t.Fatalf("got %d http hosts, want 2: %v", len(got.HTTP.Hosts), got.HTTP.Hosts)
	}

	one := got.HTTP.Hosts["interesting-hawking.example.com"]
	if one.Host != "10.64.0.2" {
		t.Errorf("host is %q, want %q", one.Host, "10.64.0.2")
	}
	if one.DefaultPort != 8000 {
		t.Errorf("default_port is %d, want 8000", one.DefaultPort)
	}
	if len(one.UnauthPorts) != 1 || one.UnauthPorts[0] != 8000 {
		t.Errorf("unauthenticated_ports is %v, want [8000]", one.UnauthPorts)
	}

	two := got.HTTP.Hosts["quirky-curie.example.com"]
	if len(two.UnauthPorts) != 2 || two.UnauthPorts[0] != 3000 || two.UnauthPorts[1] != 9090 {
		t.Errorf("unauthenticated_ports is %v, want [3000 9090]", two.UnauthPorts)
	}

	if got.HTTP.Default != "" {
		t.Errorf("http default is %q, want empty", got.HTTP.Default)
	}
}

func TestGenerateProxyConfigHTTPNoPublicPorts(t *testing.T) {
	rows := []db.ListProxyHTTPRoutesByClientRow{
		{VMName: "quirky-curie", VMIP: "10.64.0.3", HostVMID: "def456", DomainTLD: "example.com", DefaultPort: 8000},
	}

	out := generateProxyConfig(testProxyAuth, proxyHost{}, nil, rows)
	if !strings.Contains(out, "unauthenticated_ports: []") {
		t.Errorf("a vm with no published ports did not emit an empty list:\n%s", out)
	}
	got := parseProxyConfig(t, out)
	if n := len(got.HTTP.Hosts["quirky-curie.example.com"].UnauthPorts); n != 0 {
		t.Errorf("got %d unauthenticated ports, want 0", n)
	}
}

func TestGenerateProxyConfigSkipsUnusableHostnames(t *testing.T) {
	rows := []db.ListProxyHTTPRoutesByClientRow{
		{VMName: "evil\nhttp:\n  hosts: {}", VMIP: "10.64.0.2", HostVMID: "abc123", DomainTLD: "example.com", DefaultPort: 8000},
		{VMName: "quirky-curie", VMIP: "10.64.0.3", HostVMID: "def456", DomainTLD: "evil\ndefault: \"10.0.0.1:1\"", DefaultPort: 8000},
		{VMName: "peaceful-tesla", VMIP: "10.64.0.4", HostVMID: "ghi789", DomainTLD: "example.com", DefaultPort: 8000},
	}

	got := parseProxyConfig(t, generateProxyConfig(testProxyAuth, proxyHost{}, nil, rows))
	if len(got.HTTP.Hosts) != 1 {
		t.Fatalf("got %d http hosts, want 1: %v", len(got.HTTP.Hosts), got.HTTP.Hosts)
	}
	if got.HTTP.Hosts["peaceful-tesla.example.com"].Host != "10.64.0.4" {
		t.Errorf("the usable route did not survive: %v", got.HTTP.Hosts)
	}
}

func TestGenerateProxyConfigQuotesAwkwardComments(t *testing.T) {
	const awkward = `ssh-ed25519 AAAAC3Nz me@host # not a comment: "quoted" \ and: more`
	rows := []db.ListProxySSHUsersByClientRow{
		{VMIP: "10.64.0.2", HostVMID: "abc123", VMName: "build", PublicKey: awkward},
	}

	got := parseProxyConfig(t, generateProxyConfig(testProxyAuth, proxyHost{}, rows, nil))
	if len(got.SSH.Users) != 1 {
		t.Fatalf("got %d users, want 1", len(got.SSH.Users))
	}
	if got.SSH.Users[0].Pubkey != awkward {
		t.Errorf("the key did not survive:\n got %q\nwant %q", got.SSH.Users[0].Pubkey, awkward)
	}
}

func TestGenerateProxyConfigNeutralisesNamesInComments(t *testing.T) {
	rows := []db.ListProxySSHUsersByClientRow{
		{
			VMIP:      "10.64.0.2",
			HostVMID:  "abc123",
			VMName:    "evil\nssh:\n  users: []\n",
			PublicKey: "ssh-ed25519 AAAAC3Nz one",
		},
	}

	got := parseProxyConfig(t, generateProxyConfig(testProxyAuth, proxyHost{}, rows, nil))
	if len(got.SSH.Users) != 1 {
		t.Fatalf("a name broke out of its comment and changed the document: got %d users, want 1", len(got.SSH.Users))
	}
}

func TestGenerateProxyConfigAuthBlock(t *testing.T) {
	got := parseProxyConfig(t, generateProxyConfig(testProxyAuth, proxyHost{}, nil, nil))

	if want := "http://control.example.com:1323/login"; got.Auth.ControlURL != want {
		t.Errorf("control_url = %q, want %q", got.Auth.ControlURL, want)
	}
	if got.Auth.CookieSecretFile != proxyCookieSecretPath {
		t.Errorf("cookie_secret_file = %q, want %q", got.Auth.CookieSecretFile, proxyCookieSecretPath)
	}
	if got.Auth.CookieTTL != proxyCookieTTL {
		t.Errorf("cookie_ttl = %q, want %q", got.Auth.CookieTTL, proxyCookieTTL)
	}
	if got.Auth.CookieSecure {
		t.Error("cookie_secure is on for a dev control plane, so the browser will drop the cookie over http")
	}
	if got.Auth.CookieSameSite != "lax" {
		t.Errorf("cookie_samesite = %q for a dev control plane, want \"lax\"", got.Auth.CookieSameSite)
	}

	if strings.Contains(generateProxyConfig(testProxyAuth, proxyHost{}, nil, nil), testProxySecret) {
		t.Error("the shared secret was written into dproxy.yaml")
	}
}

func TestGenerateProxyConfigAuthBlockOmittedWithoutASecret(t *testing.T) {
	out := generateProxyConfig(proxyAuthConfig{controlURL: "http://control.example.com:1323"}, proxyHost{}, nil, nil)

	if strings.Contains(out, "auth:") {
		t.Errorf("an auth block was written with no key to verify tokens with:\n%s", out)
	}
	if got := parseProxyConfig(t, out); got.HTTP.Listen != proxyHTTPListen {
		t.Errorf("http listen is %q, want %q", got.HTTP.Listen, proxyHTTPListen)
	}
}

func TestGenerateProxyConfigConsoleBlock(t *testing.T) {
	got := parseProxyConfig(t, generateProxyConfig(testProxyAuth, proxyHost{}, nil, nil))

	if got.Console.Label != proxyConsoleLabel {
		t.Errorf("console label is %q, want %q", got.Console.Label, proxyConsoleLabel)
	}
	if got.Console.RemoteUser != proxyRemoteUser {
		t.Errorf("console remote_user is %q, want %q", got.Console.RemoteUser, proxyRemoteUser)
	}
}

func TestGenerateProxyConfigConsoleOmittedWithoutASecret(t *testing.T) {
	out := generateProxyConfig(proxyAuthConfig{controlURL: "http://control.example.com:1323"}, proxyHost{}, nil, nil)

	if strings.Contains(out, "console:") {
		t.Errorf("a console block was written with no key to verify tokens with:\n%s", out)
	}
}

func TestGenerateProxyConfigCookieSecureInProd(t *testing.T) {
	auth := testProxyAuth
	auth.cookieSecure = true

	got := parseProxyConfig(t, generateProxyConfig(auth, proxyHost{}, nil, nil))
	if !got.Auth.CookieSecure {
		t.Error("cookie_secure is off in prod")
	}
	if got.Auth.CookieSameSite != "none" {
		t.Errorf("cookie_samesite = %q in prod, want \"none\"", got.Auth.CookieSameSite)
	}
}

func TestGenerateProxyConfigWithoutACertificate(t *testing.T) {
	out := generateProxyConfig(testProxyAuth, proxyHost{}, nil, nil)

	if strings.Contains(out, "https:") {
		t.Errorf("an https ingress was written for a host with no certificate:\n%s", out)
	}
	if got := parseProxyConfig(t, out); got.HTTPS.Listen != "" {
		t.Errorf("https.listen = %q, want it absent", got.HTTPS.Listen)
	}
}

func TestGenerateProxyConfigSite(t *testing.T) {
	got := parseProxyConfig(t, generateProxyConfig(testProxyAuth, proxyHost{tld: "example.com"}, nil, nil))

	if len(got.Site.Hosts) != 1 || got.Site.Hosts[0] != "www.example.com" {
		t.Errorf("site.hosts = %v, want [www.example.com]", got.Site.Hosts)
	}
	if got.Site.Listen != proxySiteListen {
		t.Errorf("site.listen = %q, want %q", got.Site.Listen, proxySiteListen)
	}
	if got.Site.HTMLFile != proxySitePath {
		t.Errorf("site.html_file = %q, want %q", got.Site.HTMLFile, proxySitePath)
	}
}

func TestGenerateProxyConfigSiteAbsentWithoutADomain(t *testing.T) {
	out := generateProxyConfig(testProxyAuth, proxyHost{}, nil, nil)

	if strings.Contains(out, "site:") {
		t.Errorf("a site block was written for a host with no domain:\n%s", out)
	}
}

func TestProxySitePage(t *testing.T) {
	page := proxySitePage("http://control.example.com:1323/")

	if strings.Contains(page, proxySiteControlPlaceholder) {
		t.Error("the control url placeholder survived into the page")
	}
	if !strings.Contains(page, `href="http://control.example.com:1323"`) {
		t.Errorf("the console link is not in the page:\n%s", page)
	}
	if !strings.Contains(page, "https://github.com/getdummie/dummie") {
		t.Error("the repository link is not in the page")
	}
}

func TestProxySitePageEscapesTheControlURL(t *testing.T) {
	page := proxySitePage(`http://x/"><script>alert(1)</script>`)

	if strings.Contains(page, "<script>alert(1)</script>") {
		t.Errorf("the control url was not escaped:\n%s", page)
	}
}

func TestGenerateProxyConfigWithACertificate(t *testing.T) {
	got := parseProxyConfig(t, generateProxyConfig(testProxyAuth, proxyHost{tls: true}, nil, nil))

	if got.HTTPS.Listen != proxyHTTPSListen {
		t.Errorf("https.listen = %q, want %q", got.HTTPS.Listen, proxyHTTPSListen)
	}
	if !got.HTTPS.Reuseport {
		t.Error("https.reuseport is off, so a restart cannot bind before the old process is gone")
	}
	if got.HTTP.Listen != proxyHTTPListen {
		t.Errorf("http.listen = %q, want %q", got.HTTP.Listen, proxyHTTPListen)
	}
}

func TestGenerateProxyConfigCookieFollowsTheCertificate(t *testing.T) {
	auth := testProxyAuth
	auth.cookieSecure = false

	got := parseProxyConfig(t, generateProxyConfig(auth, proxyHost{tls: true}, nil, nil))
	if !got.Auth.CookieSecure {
		t.Error("cookie_secure is off on a host serving https")
	}
	if got.Auth.CookieSameSite != "none" {
		t.Errorf("cookie_samesite = %q, want \"none\" once the cookie is Secure", got.Auth.CookieSameSite)
	}
}

func TestGenerateProxyConfigCookieStaysOpenWithoutACertificate(t *testing.T) {
	auth := testProxyAuth
	auth.cookieSecure = false

	got := parseProxyConfig(t, generateProxyConfig(auth, proxyHost{}, nil, nil))
	if got.Auth.CookieSecure {
		t.Error("cookie_secure is on for a host serving plain http; the browser would drop the cookie")
	}
	if got.Auth.CookieSameSite != "lax" {
		t.Errorf("cookie_samesite = %q, want \"lax\"", got.Auth.CookieSameSite)
	}
}

func TestGenerateProxyConfigIsDeterministic(t *testing.T) {
	rows := []db.ListProxySSHUsersByClientRow{
		{VMIP: "10.64.0.2", HostVMID: "abc123", VMName: "build", PublicKey: "ssh-ed25519 AAAAC3Nz one"},
		{VMIP: "10.64.0.3", HostVMID: "def456", VMName: "test", PublicKey: "ssh-ed25519 AAAAC3Ny two"},
	}
	httpRows := []db.ListProxyHTTPRoutesByClientRow{
		{VMName: "interesting-hawking", VMIP: "10.64.0.2", HostVMID: "abc123", DomainTLD: "example.com", DefaultPort: 8000, PublicPorts: []int32{8000}},
		{VMName: "quirky-curie", VMIP: "10.64.0.3", HostVMID: "def456", DomainTLD: "example.com", DefaultPort: 8000},
	}
	if generateProxyConfig(testProxyAuth, proxyHost{}, rows, httpRows) != generateProxyConfig(testProxyAuth, proxyHost{}, rows, httpRows) {
		t.Error("two runs over the same rows produced different files")
	}
}

func TestRandomVMNameIsValid(t *testing.T) {
	seen := make(map[string]int)
	for range 200 {
		name, err := randomVMName()
		if err != nil {
			t.Fatalf("could not generate a name: %v", err)
		}
		if got, err := validateVMName(name); err != nil || got != name {
			t.Fatalf("generated name %q does not pass validation: %v", name, err)
		}
		if strings.Count(name, "-") != 1 {
			t.Fatalf("%q is not two hyphen-joined words", name)
		}
		seen[name]++
	}
	if len(seen) < 100 {
		t.Errorf("200 draws produced only %d distinct names", len(seen))
	}
}

func TestValidateVMName(t *testing.T) {
	if got, err := validateVMName("  "); err != nil || got != "" {
		t.Errorf("blank name gave (%q, %v), want (\"\", nil)", got, err)
	}
	for _, name := range []string{"hello-kitty", "hello-temporal-kitty", "hellokitty", "vm2", "a-1-b"} {
		if got, err := validateVMName(name); err != nil || got != name {
			t.Errorf("%q was rejected: %v", name, err)
		}
	}
	for _, name := range []string{
		"hello--kitty", "-kitty", "kitty-", "Hello-Kitty", "hello kitty", "hello.kitty",
		"hello_kitty", "ab", strings.Repeat("a", 53),
	} {
		if _, err := validateVMName(name); err == nil {
			t.Errorf("%q was accepted", name)
		}
	}
}

func TestNormalizePorts(t *testing.T) {
	if def, ports, err := normalizePorts(0, nil); err != nil || def != defaultVMPort || len(ports) != 0 {
		t.Errorf("empty request gave (%d, %v, %v), want (%d, [], nil)", def, ports, err, defaultVMPort)
	}
	def, ports, err := normalizePorts(3000, []int32{9090, 3000, 9090})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if def != 3000 || len(ports) != 2 || ports[0] != 9090 || ports[1] != 3000 {
		t.Errorf("got (%d, %v), want (3000, [9090 3000])", def, ports)
	}
	for _, bad := range [][]int32{{0}, {-1}, {65536}} {
		if _, _, err := normalizePorts(0, bad); err == nil {
			t.Errorf("public port %v was accepted", bad)
		}
	}
	if _, _, err := normalizePorts(65536, nil); err == nil {
		t.Error("default port 65536 was accepted")
	}
}

func TestGenerateProxyConfigQuotesVMNames(t *testing.T) {
	const awkward = "evil\nssh:\n  users: []"
	rows := []db.ListProxySSHUsersByClientRow{
		{VMIP: "10.64.0.2", HostVMID: "abc123", VMName: awkward, PublicKey: "ssh-ed25519 AAAAC3Nz"},
	}

	got := parseProxyConfig(t, generateProxyConfig(testProxyAuth, proxyHost{}, rows, nil))
	if len(got.SSH.Users) != 1 {
		t.Fatalf("got %d users, want 1", len(got.SSH.Users))
	}
	if got.SSH.Users[0].VMName != awkward {
		t.Errorf("the name did not survive:\n got %q\nwant %q", got.SSH.Users[0].VMName, awkward)
	}
}
