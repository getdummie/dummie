package proxy

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeSecret writes a usable cookie secret to a temp file and returns its path.
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

func TestVerifyRejects(t *testing.T) {
	a := newTestAuth(t)
	tok := a.Mint("cc@example.com", "one.vm.local")

	// A token minted for one host must not work on another.
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

// The https ingress applies the auth policy through resolve{kind:"http"}, so a
// protected host and an https listener are a valid combination.
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
