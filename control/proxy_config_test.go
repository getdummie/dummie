package main

import (
  "strings"
  "testing"

  "gopkg.in/yaml.v3"

  "control/internal/db"
)

// testProxyAuth is a fully configured hand-off, so what these tests generate is
// the file a real fleet gets rather than a stripped-down one.
var testProxyAuth = proxyAuthConfig{
  secret:     testProxySecret,
  controlURL: "http://control.example.com:1323",
}

// parsedProxyConfig is the part of the generated file these tests assert on.
// Parsed rather than string-matched, because what matters is what proxy reads,
// not how it was laid out.
type parsedProxyConfig struct {
  Auth struct {
    ControlURL       string `yaml:"control_url"`
    CookieSecretFile string `yaml:"cookie_secret_file"`
    CookieTTL        string `yaml:"cookie_ttl"`
    CookieSecure     bool   `yaml:"cookie_secure"`
  } `yaml:"auth"`
  SSH struct {
    Listen string `yaml:"listen"`
    Users  []struct {
      Pubkey     string `yaml:"pubkey"`
      Target     string `yaml:"target"`
      RemoteUser string `yaml:"remote_user"`
    } `yaml:"users"`
  } `yaml:"ssh"`
  HTTP struct {
    Listen string `yaml:"listen"`
    Hosts  map[string]struct {
      Host        string `yaml:"host"`
      // The emitted key, which is deliberately not the name the column, the
      // API or the UI uses for the same list.
      UnauthPorts []int  `yaml:"unauthenticated_ports"`
      DefaultPort int    `yaml:"default_port"`
    } `yaml:"hosts"`
    Default string `yaml:"default"`
  } `yaml:"http"`
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
  rows := []db.ListProxySSHUsersByAgentRow{
    {VMIP: "10.64.0.2", HostVMID: "abc123", VMName: "build", PublicKey: "ssh-ed25519 AAAAC3Nz one"},
    {VMIP: "10.64.0.3", HostVMID: "def456", VMName: "test", PublicKey: "ssh-ed25519 AAAAC3Ny two"},
  }

  got := parseProxyConfig(t, generateProxyConfig(testProxyAuth, rows, nil))
  if got.SSH.Listen != proxySSHListen {
    t.Errorf("listen is %q, want %q", got.SSH.Listen, proxySSHListen)
  }
  if len(got.SSH.Users) != 2 {
    t.Fatalf("got %d users, want 2", len(got.SSH.Users))
  }
  for i, want := range []struct{ pubkey, target string }{
    {"ssh-ed25519 AAAAC3Nz one", "10.64.0.2:22"},
    {"ssh-ed25519 AAAAC3Ny two", "10.64.0.3:22"},
  } {
    u := got.SSH.Users[i]
    if u.Pubkey != want.pubkey {
      t.Errorf("user %d pubkey is %q, want %q", i, u.Pubkey, want.pubkey)
    }
    if u.Target != want.target {
      t.Errorf("user %d target is %q, want %q", i, u.Target, want.target)
    }
    if u.RemoteUser != proxyRemoteUser {
      t.Errorf("user %d remote_user is %q, want %q", i, u.RemoteUser, proxyRemoteUser)
    }
  }
}

// A host whose VMs were all destroyed must produce empty collections, not
// missing keys: "nothing is published" and "not configured" should not look
// alike.
func TestGenerateProxyConfigEmptyHost(t *testing.T) {
  out := generateProxyConfig(testProxyAuth, nil, nil)
  if !strings.Contains(out, "users: []") {
    t.Errorf("an empty host did not emit an empty user list:\n%s", out)
  }
  if !strings.Contains(out, "hosts: {}") {
    t.Errorf("an empty host did not emit an empty host table:\n%s", out)
  }
  got := parseProxyConfig(t, out)
  if len(got.SSH.Users) != 0 {
    t.Errorf("got %d users, want 0", len(got.SSH.Users))
  }
  if len(got.HTTP.Hosts) != 0 {
    t.Errorf("got %d http hosts, want 0", len(got.HTTP.Hosts))
  }
}

// One http entry per VM, keyed by the VM's name under the agent's domain and
// carrying its address, the ports it publishes and the one a request goes to
// when nothing picks.
func TestGenerateProxyConfigHTTPHosts(t *testing.T) {
  rows := []db.ListProxyHTTPRoutesByAgentRow{
    {VMName: "interesting-hawking", VMIP: "10.64.0.2", HostVMID: "abc123", DomainTLD: "example.com", DefaultPort: 8000, PublicPorts: []int32{8000}},
    {VMName: "quirky-curie", VMIP: "10.64.0.3", HostVMID: "def456", DomainTLD: "example.com", DefaultPort: 3000, PublicPorts: []int32{3000, 9090}},
  }

  got := parseProxyConfig(t, generateProxyConfig(testProxyAuth, nil, rows))
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

  // The list is written in the order it was given, not sorted: that order is the
  // caller's, and reordering it would show up as a diff on every host.
  two := got.HTTP.Hosts["quirky-curie.example.com"]
  if len(two.UnauthPorts) != 2 || two.UnauthPorts[0] != 3000 || two.UnauthPorts[1] != 9090 {
    t.Errorf("unauthenticated_ports is %v, want [3000 9090]", two.UnauthPorts)
  }

  // An unmatched Host header must miss rather than land on some arbitrary guest.
  if got.HTTP.Default != "" {
    t.Errorf("http default is %q, want empty", got.HTTP.Default)
  }
}

// A VM that publishes nothing still gets an entry, with an empty list rather
// than a missing key.
func TestGenerateProxyConfigHTTPNoPublicPorts(t *testing.T) {
  rows := []db.ListProxyHTTPRoutesByAgentRow{
    {VMName: "quirky-curie", VMIP: "10.64.0.3", HostVMID: "def456", DomainTLD: "example.com", DefaultPort: 8000},
  }

  out := generateProxyConfig(testProxyAuth, nil, rows)
  if !strings.Contains(out, "unauthenticated_ports: []") {
    t.Errorf("a vm with no published ports did not emit an empty list:\n%s", out)
  }
  got := parseProxyConfig(t, out)
  if n := len(got.HTTP.Hosts["quirky-curie.example.com"].UnauthPorts); n != 0 {
    t.Errorf("got %d unauthenticated ports, want 0", n)
  }
}

// The key is the one unquoted value in the file. A row that cannot produce a
// real hostname is dropped rather than written, so it cannot introduce structure
// into the document.
func TestGenerateProxyConfigSkipsUnusableHostnames(t *testing.T) {
  rows := []db.ListProxyHTTPRoutesByAgentRow{
    {VMName: "evil\nhttp:\n  hosts: {}", VMIP: "10.64.0.2", HostVMID: "abc123", DomainTLD: "example.com", DefaultPort: 8000},
    {VMName: "quirky-curie", VMIP: "10.64.0.3", HostVMID: "def456", DomainTLD: "evil\ndefault: \"10.0.0.1:1\"", DefaultPort: 8000},
    {VMName: "peaceful-tesla", VMIP: "10.64.0.4", HostVMID: "ghi789", DomainTLD: "example.com", DefaultPort: 8000},
  }

  got := parseProxyConfig(t, generateProxyConfig(testProxyAuth, nil, rows))
  if len(got.HTTP.Hosts) != 1 {
    t.Fatalf("got %d http hosts, want 1: %v", len(got.HTTP.Hosts), got.HTTP.Hosts)
  }
  if got.HTTP.Hosts["peaceful-tesla.example.com"].Host != "10.64.0.4" {
    t.Errorf("the usable route did not survive: %v", got.HTTP.Hosts)
  }
}

// The comment on a public key is free text the user typed. Unquoted, a " #" in
// it would truncate the scalar and leave a valid-looking key that is not the one
// on file -- so the value has to survive the round trip intact.
func TestGenerateProxyConfigQuotesAwkwardComments(t *testing.T) {
  const awkward = `ssh-ed25519 AAAAC3Nz me@host # not a comment: "quoted" \ and: more`
  rows := []db.ListProxySSHUsersByAgentRow{
    {VMIP: "10.64.0.2", HostVMID: "abc123", VMName: "build", PublicKey: awkward},
  }

  got := parseProxyConfig(t, generateProxyConfig(testProxyAuth, rows, nil))
  if len(got.SSH.Users) != 1 {
    t.Fatalf("got %d users, want 1", len(got.SSH.Users))
  }
  if got.SSH.Users[0].Pubkey != awkward {
    t.Errorf("the key did not survive:\n got %q\nwant %q", got.SSH.Users[0].Pubkey, awkward)
  }
}

// A VM name ends up in a '#' comment line. A newline in one would end the
// comment and put whatever followed into the document as configuration.
func TestGenerateProxyConfigNeutralisesNamesInComments(t *testing.T) {
  rows := []db.ListProxySSHUsersByAgentRow{
    {
      VMIP:      "10.64.0.2",
      HostVMID:  "abc123",
      VMName:    "evil\nssh:\n  users: []\n",
      PublicKey: "ssh-ed25519 AAAAC3Nz one",
    },
  }

  got := parseProxyConfig(t, generateProxyConfig(testProxyAuth, rows, nil))
  if len(got.SSH.Users) != 1 {
    t.Fatalf("a name broke out of its comment and changed the document: got %d users, want 1", len(got.SSH.Users))
  }
}

// The auth block is what points a guest's proxy back at this server, so the url
// in it has to be the login path under the origin browsers reach us on -- not
// the address this process binds.
func TestGenerateProxyConfigAuthBlock(t *testing.T) {
  got := parseProxyConfig(t, generateProxyConfig(testProxyAuth, nil, nil))

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

  // The key itself is never in the file -- it is written to a 0600 file the
  // block names, and proxy.yaml is readable by anyone on the host.
  if strings.Contains(generateProxyConfig(testProxyAuth, nil, nil), testProxySecret) {
    t.Error("the shared secret was written into proxy.yaml")
  }
}

func TestGenerateProxyConfigAuthBlockOmittedWithoutASecret(t *testing.T) {
  out := generateProxyConfig(proxyAuthConfig{controlURL: "http://control.example.com:1323"}, nil, nil)

  if strings.Contains(out, "auth:") {
    t.Errorf("an auth block was written with no key to verify tokens with:\n%s", out)
  }
  // Still a whole config: the guests on the host stay reachable exactly as they
  // were before the hand-off existed.
  if got := parseProxyConfig(t, out); got.HTTP.Listen != proxyHTTPListen {
    t.Errorf("http listen is %q, want %q", got.HTTP.Listen, proxyHTTPListen)
  }
}

// A prod control plane serves guests over https, and a cookie without Secure
// there is one that travels in the clear.
func TestGenerateProxyConfigCookieSecureInProd(t *testing.T) {
  auth := testProxyAuth
  auth.cookieSecure = true

  if got := parseProxyConfig(t, generateProxyConfig(auth, nil, nil)); !got.Auth.CookieSecure {
    t.Error("cookie_secure is off in prod")
  }
}

// The agent skips the restart when the file is unchanged, which only works if
// the same fleet compiles to the same bytes every time.
func TestGenerateProxyConfigIsDeterministic(t *testing.T) {
  rows := []db.ListProxySSHUsersByAgentRow{
    {VMIP: "10.64.0.2", HostVMID: "abc123", VMName: "build", PublicKey: "ssh-ed25519 AAAAC3Nz one"},
    {VMIP: "10.64.0.3", HostVMID: "def456", VMName: "test", PublicKey: "ssh-ed25519 AAAAC3Ny two"},
  }
  httpRows := []db.ListProxyHTTPRoutesByAgentRow{
    {VMName: "interesting-hawking", VMIP: "10.64.0.2", HostVMID: "abc123", DomainTLD: "example.com", DefaultPort: 8000, PublicPorts: []int32{8000}},
    {VMName: "quirky-curie", VMIP: "10.64.0.3", HostVMID: "def456", DomainTLD: "example.com", DefaultPort: 8000},
  }
  if generateProxyConfig(testProxyAuth, rows, httpRows) != generateProxyConfig(testProxyAuth, rows, httpRows) {
    t.Error("two runs over the same rows produced different files")
  }
}

// A generated name goes into the same column a user-supplied one does, so it has
// to satisfy every rule that column enforces.
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
  // Not a uniqueness guarantee -- the database has that -- but a generator that
  // keeps returning one name would pass every check above.
  if len(seen) < 100 {
    t.Errorf("200 draws produced only %d distinct names", len(seen))
  }
}

func TestValidateVMName(t *testing.T) {
  // Empty is the request to generate one, not a rejection.
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
  // Unset means the default, so a request that omits the field and one that
  // sends 0 land on the same row.
  if def, ports, err := normalizePorts(0, nil); err != nil || def != defaultVMPort || len(ports) != 0 {
    t.Errorf("empty request gave (%d, %v, %v), want (%d, [], nil)", def, ports, err, defaultVMPort)
  }
  // Duplicates collapse, order is kept.
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
