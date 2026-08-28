package proxy

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"dproxy/internal/control"
)

func newTestKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("NewPublicKey: %v", err)
	}
	return sshPub
}

func authorizedLine(key ssh.PublicKey, comment string) string {
	return string(ssh.MarshalAuthorizedKey(key)) + comment
}

func newTestResolver(t *testing.T, cfg *Config) *Resolver {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	auth, err := NewAuthenticator(cfg.Auth)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	r, err := NewResolver(log, NewRouter(cfg), auth, cfg)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return r
}

func TestResolveSSHKnownKey(t *testing.T) {
	alice := newTestKey(t)
	cfg := &Config{
		ControlSocket: "/tmp/ignored.sock",
		SSH: &SSHConfig{Listen: ":2222", Users: []SSHUser{{
			PubKey:     authorizedLine(alice, " alice@laptop"),
			VMName:     "build",
			Target:     "127.0.0.1:22",
			RemoteUser: "dev",
		}}},
	}
	r := newTestResolver(t, cfg)

	rep := r.Handle(control.Msg{
		V: control.Version, Type: control.TypeResolve, ID: "r1", Kind: control.KindSSH,
		SSHUser: "build", SSHPubKey: authorizedLine(alice, " someone-else@host"),
		SSHFingerprint: ssh.FingerprintSHA256(alice), ClientIP: "10.0.0.2",
	})
	if rep.Type != control.TypeResolved || !rep.Authorized {
		t.Fatalf("reply = %+v", rep)
	}
	if rep.Target != "127.0.0.1:22" || rep.RemoteUser != "dev" {
		t.Fatalf("target/remote_user = %q/%q", rep.Target, rep.RemoteUser)
	}
}

func TestResolveSSHUnknownKey(t *testing.T) {
	alice, bob := newTestKey(t), newTestKey(t)
	cfg := &Config{SSH: &SSHConfig{Listen: ":2222", Users: []SSHUser{{
		PubKey: authorizedLine(alice, ""), VMName: "build", Target: "127.0.0.1:22", RemoteUser: "dev",
	}}}}
	r := newTestResolver(t, cfg)

	rep := r.Handle(control.Msg{
		V: control.Version, Type: control.TypeResolve, ID: "r2", Kind: control.KindSSH,
		SSHPubKey: authorizedLine(bob, ""),
	})
	if rep.Type != control.TypeResolved || rep.Authorized {
		t.Fatalf("unknown key must not be authorized: %+v", rep)
	}
}

func TestResolveSSHUnparsableKeyFailsClosed(t *testing.T) {
	r := newTestResolver(t, &Config{})
	rep := r.Handle(control.Msg{V: control.Version, Type: control.TypeResolve, ID: "r3",
		Kind: control.KindSSH, SSHPubKey: "not-a-key"})
	if rep.Authorized {
		t.Fatalf("expected unauthorized, got %+v", rep)
	}
}

func TestResolveHTTP(t *testing.T) {
	cfg := &Config{HTTP: &HTTPConfig{Hosts: map[string]HTTPHost{
		"one.vm.local": {Host: "127.0.0.1", DefaultPort: 8001, UnauthenticatedPorts: []int{8001}},
	}}}
	r := newTestResolver(t, cfg)

	hit := r.Handle(control.Msg{V: control.Version, Type: control.TypeResolve, ID: "h1",
		Kind: control.KindHTTP, Host: "one.vm.local", SNI: "one.vm.local"})
	if !hit.Authorized || hit.Target != "127.0.0.1:8001" {
		t.Fatalf("hit = %+v", hit)
	}

	miss := r.Handle(control.Msg{V: control.Version, Type: control.TypeResolve, ID: "h2",
		Kind: control.KindHTTP, Host: "nope.vm.local"})
	if miss.Authorized {
		t.Fatalf("miss must not be authorized: %+v", miss)
	}
}

func protectedHTTPSResolver(t *testing.T) (*Resolver, *Authenticator) {
	t.Helper()
	cfg := &Config{
		ControlSocket: "/tmp/ignored.sock",
		HTTP:          &HTTPConfig{Listen: ":8080", Hosts: map[string]HTTPHost{"one.vm.local": {Host: "127.0.0.1", DefaultPort: 8001}}},
		HTTPS:         &HTTPSConfig{Listen: ":8443"},
		Auth:          &AuthConfig{ControlURL: "https://control.local/login", CookieSecretFile: writeSecret(t), CookieSecure: true},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	r := newTestResolver(t, cfg)
	return r, r.auth
}

func resolveHTTPS(r *Resolver, m control.Msg) control.Msg {
	m.V, m.Type, m.Kind = control.Version, control.TypeResolve, control.KindHTTP
	if m.ID == "" {
		m.ID = "x1"
	}
	if m.Host == "" {
		m.Host = "one.vm.local"
	}
	return r.Handle(m)
}

func TestResolveHTTPProtectedRedirectsToLogin(t *testing.T) {
	r, _ := protectedHTTPSResolver(t)

	rep := resolveHTTPS(r, control.Msg{Path: "/dash?a=1", Accept: "text/html"})
	if rep.Authorized || rep.Target != "" {
		t.Fatalf("unauthenticated request must not be authorized: %+v", rep)
	}
	if rep.Status != http.StatusFound || !strings.HasPrefix(rep.Location, "https://control.local/login?") {
		t.Fatalf("want a redirect to the login url, got %+v", rep)
	}
	if !strings.Contains(rep.Location, url.QueryEscape("/dash?a=1")) {
		t.Fatalf("login url must carry the original target: %s", rep.Location)
	}
}

func TestResolveHTTPProtectedRejectsBadCookies(t *testing.T) {
	r, a := protectedHTTPSResolver(t)
	name := a.CookieName()

	cases := []struct{ what, cookie string }{
		{"absent", ""},
		{"garbage", name + "=garbage"},
		{"forged", name + "=" + strings.Replace(a.Mint("eve", "one.vm.local"), ".", ".x", 1)},
		{"other host", name + "=" + a.Mint("eve", "two.vm.local")},
		{"console audience", name + "=" + a.Mint("eve", consoleAudPrefix+"one.vm.local")},
	}
	for _, c := range cases {
		rep := resolveHTTPS(r, control.Msg{Path: "/", Cookie: c.cookie, Accept: "text/html"})
		if rep.Authorized || rep.Target != "" {
			t.Fatalf("%s cookie was authorized: %+v", c.what, rep)
		}
		if rep.Status != http.StatusFound {
			t.Fatalf("%s cookie: status = %d, want 302", c.what, rep.Status)
		}
	}
}

func TestResolveHTTPProtectedAcceptsSessionCookie(t *testing.T) {
	r, a := protectedHTTPSResolver(t)

	rep := resolveHTTPS(r, control.Msg{Path: "/", Cookie: a.CookieName() + "=" + a.Mint("alice", "one.vm.local")})
	if !rep.Authorized || rep.Target != "127.0.0.1:8001" {
		t.Fatalf("valid session must be routed: %+v", rep)
	}
	if rep.Status != 0 || rep.Location != "" {
		t.Fatalf("routed request must carry no response: %+v", rep)
	}
}

func TestResolveHTTPNonBrowserGets401(t *testing.T) {
	r, _ := protectedHTTPSResolver(t)

	rep := resolveHTTPS(r, control.Msg{Path: "/api", Accept: "application/json"})
	if rep.Status != http.StatusUnauthorized || rep.Location != "" {
		t.Fatalf("api client must get a bare 401: %+v", rep)
	}
}

func TestResolveHTTPCallbackMintsSession(t *testing.T) {
	r, a := protectedHTTPSResolver(t)

	token := a.Mint("alice", "one.vm.local")
	rep := resolveHTTPS(r, control.Msg{
		Path:   CallbackPath + "?token=" + url.QueryEscape(token) + "&next=" + url.QueryEscape("/dash"),
		Accept: "text/html",
	})
	if rep.Authorized || rep.Status != http.StatusFound || rep.Location != "/dash" {
		t.Fatalf("callback = %+v, want a 302 to /dash", rep)
	}
	if !strings.HasPrefix(rep.SetCookie, a.CookieName()+"=") {
		t.Fatalf("callback must set the session cookie: %q", rep.SetCookie)
	}

	c, err := http.ParseSetCookie(rep.SetCookie)
	if err != nil {
		t.Fatalf("ParseSetCookie: %v", err)
	}
	if next := resolveHTTPS(r, control.Msg{Path: "/dash", Cookie: c.Name + "=" + c.Value}); !next.Authorized {
		t.Fatalf("minted session was not accepted: %+v", next)
	}
}

func TestResolveHTTPCallbackRejectsForgedToken(t *testing.T) {
	r, _ := protectedHTTPSResolver(t)

	rep := resolveHTTPS(r, control.Msg{Path: CallbackPath + "?token=forged&next=/dash", Accept: "text/html"})
	if rep.Status != http.StatusUnauthorized || rep.SetCookie != "" {
		t.Fatalf("forged callback token = %+v, want 401 and no cookie", rep)
	}
}

func consoleResolver(t *testing.T) (*Resolver, *Authenticator) {
	t.Helper()
	cfg := &Config{
		ControlSocket: "/tmp/ignored.sock",
		HTTP:          &HTTPConfig{Listen: ":8080", Hosts: map[string]HTTPHost{"one.vm.local": {Host: "10.64.0.2", DefaultPort: 8000}}},
		HTTPS:         &HTTPSConfig{Listen: ":8443"},
		Auth:          &AuthConfig{ControlURL: "https://control.local/login", CookieSecretFile: writeSecret(t), CookieSecure: true},
		Console:       &ConsoleConfig{Label: "shell", RemoteUser: "ubuntu"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	r := newTestResolver(t, cfg)
	return r, r.auth
}

func wsResolve(token string) control.Msg {
	return control.Msg{
		Host:       "one.shell.vm.local",
		Path:       "/?token=" + url.QueryEscape(token),
		Upgrade:    "websocket",
		Connection: "Upgrade",
		WSVersion:  "13",
		WSKey:      "dGhlIHNhbXBsZSBub25jZQ==",
	}
}

func TestResolveConsoleOverHTTPS(t *testing.T) {
	r, a := consoleResolver(t)

	m := wsResolve(a.Mint("alice", consoleAudPrefix+"one.shell.vm.local"))
	rep := resolveHTTPS(r, m)
	if !rep.Authorized || rep.Protocol != control.ProtoConsole {
		t.Fatalf("console resolve = %+v, want an authorized console", rep)
	}
	if rep.Target != "10.64.0.2:22" || rep.RemoteUser != "ubuntu" || rep.Sub != "alice" {
		t.Fatalf("console reply = {target:%q remote_user:%q sub:%q}", rep.Target, rep.RemoteUser, rep.Sub)
	}
	if rep.WSKey != m.WSKey {
		t.Fatalf("ws key = %q, want the client's %q", rep.WSKey, m.WSKey)
	}
}

func TestResolveConsoleOverHTTPSRefusals(t *testing.T) {
	r, a := consoleResolver(t)
	good := a.Mint("alice", consoleAudPrefix+"one.shell.vm.local")

	session := a.Mint("alice", "one.vm.local")

	cases := []struct {
		what   string
		msg    control.Msg
		status int
	}{
		{"no token", wsResolve(""), http.StatusUnauthorized},
		{"forged token", wsResolve(good + "x"), http.StatusUnauthorized},
		{"session token", wsResolve(session), http.StatusUnauthorized},
		{"console token for another host", wsResolve(a.Mint("alice", consoleAudPrefix+"two.shell.vm.local")), http.StatusUnauthorized},
		{"not a websocket upgrade", control.Msg{Host: "one.shell.vm.local", Path: "/?token=" + good}, http.StatusNotFound},
	}
	for _, c := range cases {
		rep := resolveHTTPS(r, c.msg)
		if rep.Authorized || rep.Target != "" || rep.Protocol != "" {
			t.Fatalf("%s was authorized: %+v", c.what, rep)
		}
		if rep.Status != c.status {
			t.Fatalf("%s: status = %d, want %d", c.what, rep.Status, c.status)
		}
	}
}

func TestResolveConsoleNameForUnpublishedVMIsUnknown(t *testing.T) {
	r, a := consoleResolver(t)

	m := wsResolve(a.Mint("alice", consoleAudPrefix+"nope.shell.vm.local"))
	m.Host = "nope.shell.vm.local"
	rep := resolveHTTPS(r, m)
	if rep.Authorized || rep.Status != 0 {
		t.Fatalf("a console name whose VM is not published must be an unknown host: %+v", rep)
	}
}

func TestResolveHTTPUnparsablePathFailsClosed(t *testing.T) {
	r, a := protectedHTTPSResolver(t)

	rep := resolveHTTPS(r, control.Msg{Path: "not a uri", Cookie: a.CookieName() + "=" + a.Mint("alice", "one.vm.local")})
	if rep.Authorized || rep.Status != http.StatusBadRequest {
		t.Fatalf("unparsable path = %+v, want 400 and no route", rep)
	}
}

func TestResolveUnknownKind(t *testing.T) {
	r := newTestResolver(t, &Config{})
	rep := r.Handle(control.Msg{V: control.Version, Type: control.TypeResolve, ID: "k1", Kind: "smtp"})
	if rep.Type != control.TypeError {
		t.Fatalf("reply = %+v, want error", rep)
	}
}

func TestDuplicateKeyAndVMRejected(t *testing.T) {
	alice := newTestKey(t)
	line := authorizedLine(alice, "")
	cfg := &Config{SSH: &SSHConfig{Listen: ":2222", Users: []SSHUser{
		{PubKey: line, VMName: "build", Target: "127.0.0.1:22", RemoteUser: "dev"},
		{PubKey: line, VMName: "build", Target: "127.0.0.1:2200", RemoteUser: "root"},
	}}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := NewResolver(log, NewRouter(cfg), nil, cfg); err == nil {
		t.Fatal("expected the same key and vm name twice to be rejected")
	}
}

func TestValidateRejectsUnusableVMNames(t *testing.T) {
	line := authorizedLine(newTestKey(t), "")
	for name, vm := range map[string]string{"empty": "", "with a space": "my vm", "with an at": "vm@host"} {
		t.Run(name, func(t *testing.T) {
			cfg := &Config{ControlSocket: "/tmp/ignored.sock", SSH: &SSHConfig{Listen: ":2222", Users: []SSHUser{
				{PubKey: line, VMName: vm, Target: "10.64.0.2:22", RemoteUser: "ubuntu"},
			}}}
			if err := cfg.Validate(); err == nil {
				t.Errorf("vm_name %q was accepted", vm)
			}
		})
	}
}

func TestResolveSSHOneKeyManyVMs(t *testing.T) {
	alice := newTestKey(t)
	line := authorizedLine(alice, " alice@laptop")
	cfg := &Config{SSH: &SSHConfig{Listen: ":2222", Users: []SSHUser{
		{PubKey: line, VMName: "build", Target: "10.64.0.2:22", RemoteUser: "ubuntu"},
		{PubKey: line, VMName: "test", Target: "10.64.0.3:22", RemoteUser: "ubuntu"},
	}}}
	r := newTestResolver(t, cfg)

	for vm, want := range map[string]string{"build": "10.64.0.2:22", "test": "10.64.0.3:22"} {
		rep := r.Handle(control.Msg{V: control.Version, Type: control.TypeResolve, ID: "m1",
			Kind: control.KindSSH, SSHUser: vm, SSHPubKey: line})
		if !rep.Authorized || rep.Target != want {
			t.Errorf("%s resolved to %+v, want target %q", vm, rep, want)
		}
	}
}

func TestResolveSSHNoVMSelected(t *testing.T) {
	alice := newTestKey(t)
	line := authorizedLine(alice, "")
	cfg := &Config{SSH: &SSHConfig{Listen: ":2222", Users: []SSHUser{
		{PubKey: line, VMName: "build", Target: "10.64.0.2:22", RemoteUser: "ubuntu"},
		{PubKey: line, VMName: "test", Target: "10.64.0.3:22", RemoteUser: "ubuntu"},
	}}}
	r := newTestResolver(t, cfg)

	rep := r.Handle(control.Msg{V: control.Version, Type: control.TypeResolve, ID: "n1",
		Kind: control.KindSSH, SSHUser: "ameya", SSHPubKey: line})
	if rep.Authorized || rep.Target != "" {
		t.Fatalf("a bare login must not route anywhere: %+v", rep)
	}
	if !strings.Contains(rep.Notice, "build") || !strings.Contains(rep.Notice, "test") {
		t.Errorf("notice must list the vms this key owns, got %q", rep.Notice)
	}
	if len(rep.Notice) > control.MaxNotice {
		t.Errorf("notice is %d bytes, over MaxNotice", len(rep.Notice))
	}
}

func TestResolveSSHUnknownKeyGetsNoNotice(t *testing.T) {
	alice, bob := newTestKey(t), newTestKey(t)
	cfg := &Config{SSH: &SSHConfig{Listen: ":2222", Users: []SSHUser{
		{PubKey: authorizedLine(alice, ""), VMName: "build", Target: "10.64.0.2:22", RemoteUser: "ubuntu"},
	}}}
	r := newTestResolver(t, cfg)

	rep := r.Handle(control.Msg{V: control.Version, Type: control.TypeResolve, ID: "n2",
		Kind: control.KindSSH, SSHUser: "build", SSHPubKey: authorizedLine(bob, "")})
	if rep.Authorized || rep.Notice != "" {
		t.Fatalf("unknown key must get a bare denial: %+v", rep)
	}
}

func TestResolveSSHOtherUsersVM(t *testing.T) {
	alice, bob := newTestKey(t), newTestKey(t)
	cfg := &Config{SSH: &SSHConfig{Listen: ":2222", Users: []SSHUser{
		{PubKey: authorizedLine(alice, ""), VMName: "alices", Target: "10.64.0.2:22", RemoteUser: "ubuntu"},
		{PubKey: authorizedLine(bob, ""), VMName: "bobs", Target: "10.64.0.3:22", RemoteUser: "ubuntu"},
	}}}
	r := newTestResolver(t, cfg)

	rep := r.Handle(control.Msg{V: control.Version, Type: control.TypeResolve, ID: "n3",
		Kind: control.KindSSH, SSHUser: "bobs", SSHPubKey: authorizedLine(alice, "")})
	if rep.Authorized || rep.Target != "" {
		t.Fatalf("alice must not reach bob's vm: %+v", rep)
	}
	if strings.Contains(rep.Notice, "bobs") {
		t.Errorf("the notice must not name another user's vm: %q", rep.Notice)
	}
}
