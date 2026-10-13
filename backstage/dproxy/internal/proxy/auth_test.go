package proxy

import (
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeSecret(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	return path
}

func newTestAuth(t *testing.T) *Authenticator {
	t.Helper()
	a, err := NewAuthenticator(&AuthConfig{
		ControlURL:       "http://control.local/login",
		CookieSecretFile: writeSecret(t),
		CookieTTL:        Duration(time.Hour),
	})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	return a
}

func TestVerifyRoundTrip(t *testing.T) {
	a := newTestAuth(t)
	tok := a.Mint("cc@example.com", "one.vm.local")
	sub, ok := a.Verify(tok, "one.vm.local")
	if !ok || sub != "cc@example.com" {
		t.Fatalf("Verify = (%q, %v)", sub, ok)
	}
}

func TestProtectedHostLoginKeepsForwardedPort(t *testing.T) {
	a := newTestAuth(t)
	router := NewRouter(&Config{HTTP: &HTTPConfig{Hosts: map[string]HTTPHost{
		"one.vm.local": {Host: "10.64.0.2", DefaultPort: 8000},
	}}})
	req, err := http.NewRequest(http.MethodGet, "http://one.vm.local:8080/dash", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/html")
	verdict := authorizeRequest(slog.Default(), router, a, "one.vm.local", req)
	if verdict.status != http.StatusFound {
		t.Fatalf("login verdict = %+v, want redirect", verdict)
	}
	loginURL, err := url.Parse(verdict.location)
	if err != nil {
		t.Fatal(err)
	}
	if got := loginURL.Query().Get("host"); got != "one.vm.local" {
		t.Fatalf("token audience host = %q, want one.vm.local", got)
	}
	if got := loginURL.Query().Get("rd"); got != "http://one.vm.local:8080"+CallbackPath {
		t.Fatalf("callback URL = %q, want the forwarded port", got)
	}

	req.Host = "other.vm.local:8080"
	verdict = authorizeRequest(slog.Default(), router, a, "one.vm.local", req)
	loginURL, err = url.Parse(verdict.location)
	if err != nil {
		t.Fatal(err)
	}
	if got := loginURL.Query().Get("rd"); got != "http://one.vm.local"+CallbackPath {
		t.Fatalf("mismatched authority redirected to %q", got)
	}
}

func TestVerifyRejects(t *testing.T) {
	a := newTestAuth(t)
	tok := a.Mint("cc@example.com", "one.vm.local")

	if _, ok := a.Verify(tok, "two.vm.local"); ok {
		t.Error("token replayed across hosts")
	}
	if _, ok := a.Verify(tok[:len(tok)-1]+"x", "one.vm.local"); ok {
		t.Error("tampered signature accepted")
	}
	if _, ok := a.Verify("garbage", "one.vm.local"); ok {
		t.Error("malformed token accepted")
	}
	if _, ok := a.Verify("", "one.vm.local"); ok {
		t.Error("empty token accepted")
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	a := newTestAuth(t)
	tok := a.Mint("cc@example.com", "one.vm.local")
	a.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if _, ok := a.Verify(tok, "one.vm.local"); ok {
		t.Fatal("expired token accepted")
	}
}

func TestShortSecretRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("tooshort"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	if _, err := NewAuthenticator(&AuthConfig{ControlURL: "http://c/l", CookieSecretFile: path}); err == nil {
		t.Fatal("expected a short secret to be rejected")
	}
}

func TestProtectedHostWithoutAuthConfigIsRejected(t *testing.T) {
	cfg := &Config{
		ControlSocket: "/run/dpipe/control.sock",
		HTTP: &HTTPConfig{Listen: "0.0.0.0:80", Hosts: map[string]HTTPHost{
			"two.vm.local": {Host: "10.64.0.2", DefaultPort: 8000},
		}},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("a protected host with no auth: block must fail validation")
	}
}

func TestProtectedHostWithHTTPSIsAccepted(t *testing.T) {
	cfg := &Config{
		ControlSocket: "/run/dpipe/control.sock",
		HTTP: &HTTPConfig{Listen: "0.0.0.0:80", Hosts: map[string]HTTPHost{
			"two.vm.local": {Host: "10.64.0.2", DefaultPort: 8000},
		}},
		HTTPS: &HTTPSConfig{Listen: "0.0.0.0:443"},
		Auth: &AuthConfig{
			ControlURL:       "https://control.local/login",
			CookieSecretFile: writeSecret(t),
			CookieSecure:     true,
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestHostKeyWithPortSeparatorRejected(t *testing.T) {
	cfg := &Config{
		ControlSocket: "/run/dpipe/control.sock",
		HTTP: &HTTPConfig{Listen: "0.0.0.0:80", Hosts: map[string]HTTPHost{
			"one--9922.vm.local": {Host: "10.64.0.2", UnauthenticatedPorts: []int{9922}, DefaultPort: 9922},
		}},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("a host key containing -- must fail validation")
	}
}
