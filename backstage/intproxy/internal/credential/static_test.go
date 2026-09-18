package credential

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tokenFile(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pat")
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStaticSingleToken(t *testing.T) {
	s, err := NewStatic(StaticConfig{TokenRef: TokenRef{File: tokenFile(t, "ghp_one\n", 0o600)}})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := s.Token(context.Background(), Scope{Integration: "github"})
	if err != nil {
		t.Fatal(err)
	}
	if tok.Value != "ghp_one" {
		t.Fatalf("token = %q, want the trimmed file contents", tok.Value)
	}
	if !tok.ExpiresAt.IsZero() {
		t.Fatal("a static token must not carry an expiry it cannot honour")
	}
}

func TestStaticPerClientCredentials(t *testing.T) {
	s, err := NewStatic(StaticConfig{
		Credentials: map[string]TokenRef{
			"ci":  {File: tokenFile(t, "ghp_ci", 0o600)},
			"dev": {File: tokenFile(t, "ghp_dev", 0o600)},
		},
		Default: "dev",
	})
	if err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{"ci": "ghp_ci", "dev": "ghp_dev", "": "ghp_dev"} {
		tok, err := s.Token(context.Background(), Scope{Credential: name})
		if err != nil {
			t.Fatalf("credential %q: %v", name, err)
		}
		if tok.Value != want {
			t.Fatalf("credential %q = %q, want %q", name, tok.Value, want)
		}
	}
}

func TestStaticUnknownCredentialIsRefused(t *testing.T) {
	s, err := NewStatic(StaticConfig{
		Credentials: map[string]TokenRef{"ci": {File: tokenFile(t, "ghp_ci", 0o600)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Token(context.Background(), Scope{Credential: "nope"}); err == nil {
		t.Fatal("an unconfigured credential was served")
	}
}

// A token file the whole machine can read is not a secret, and saying so at
// startup beats discovering it later.
func TestStaticRefusesAReadableTokenFile(t *testing.T) {
	_, err := NewStatic(StaticConfig{TokenRef: TokenRef{File: tokenFile(t, "ghp_one", 0o644)}})
	if err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("a world-readable token file was accepted: %v", err)
	}
}

func TestStaticFromEnv(t *testing.T) {
	t.Setenv("INTPROXY_TEST_PAT", " ghp_env ")
	s, err := NewStatic(StaticConfig{TokenRef: TokenRef{Env: "INTPROXY_TEST_PAT"}})
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := s.Token(context.Background(), Scope{})
	if tok.Value != "ghp_env" {
		t.Fatalf("token = %q", tok.Value)
	}
}

func TestStaticRejects(t *testing.T) {
	cases := []struct {
		name string
		cfg  StaticConfig
		want string
	}{
		{"nothing", StaticConfig{}, "no credentials are configured"},
		{"both forms", StaticConfig{
			Credentials: map[string]TokenRef{"ci": {Env: "INTPROXY_TEST_PAT"}},
			TokenRef:    TokenRef{Env: "INTPROXY_TEST_PAT"},
		}, "not both"},
		{"file and env", StaticConfig{
			TokenRef: TokenRef{File: "/nope", Env: "INTPROXY_TEST_PAT"},
		}, "token_file or token_env, not both"},
		{"missing default", StaticConfig{
			Credentials: map[string]TokenRef{"ci": {Env: "INTPROXY_TEST_PAT"}, "dev": {Env: "INTPROXY_TEST_PAT"}},
			Default:     "nope",
		}, "is not in the credentials map"},
		{"unsupported kind", StaticConfig{
			Kind:     "app",
			TokenRef: TokenRef{Env: "INTPROXY_TEST_PAT"},
		}, "is not supported yet"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("INTPROXY_TEST_PAT", "ghp_env")
			_, err := NewStatic(tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}
