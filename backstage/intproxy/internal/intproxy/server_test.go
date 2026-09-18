package intproxy

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"intproxy/internal/credential"
	"intproxy/internal/integration"
	"intproxy/internal/policy"
)

type fakeSource struct {
	fn func(credential.Scope) (credential.Token, error)
}

func (f fakeSource) Token(_ context.Context, s credential.Scope) (credential.Token, error) {
	return f.fn(s)
}

func minting(token string) fakeSource {
	return fakeSource{fn: func(credential.Scope) (credential.Token, error) {
		return credential.Token{Value: token, ExpiresAt: time.Now().Add(time.Hour)}, nil
	}}
}

func denying(status int, msg string) fakeSource {
	return fakeSource{fn: func(credential.Scope) (credential.Token, error) {
		return credential.Token{}, &credential.Denial{Status: status, Message: msg}
	}}
}

// testServer assembles a Server the way New does, minus the certificate and
// the credential wiring, so a test can supply its own source.
func testServer(t *testing.T, src credential.Source, ghOptions string) *Server {
	t.Helper()

	cfg := testConfig()
	off := false
	cfg.TLS.Enabled = &off

	s := &Server{
		cfg:     cfg,
		log:     slog.New(slog.DiscardHandler),
		version: "test",
		hosts:   map[string]integration.Integration{},
	}
	s.creds = credential.NewCache(src, time.Minute)

	var decode func(any) error
	if ghOptions != "" {
		decode = func(v any) error { return yaml.Unmarshal([]byte(ghOptions), v) }
	}

	host := cfg.hostname("github")
	ig, err := integration.New("github", integration.Options{
		Decode:  decode,
		DocsURL: cfg.DocsURL,
		SelfURL: func() string { return s.selfURL(host) },
		Version: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	s.hosts[host] = ig
	s.names = []string{host}
	s.rp = s.newReverseProxy()
	return s
}

func githubHost() string { return "github.int.example.com" }

func TestServeRejectsForeignHost(t *testing.T) {
	s := testServer(t, minting("ghs_x"), "")
	r := httptest.NewRequest(http.MethodGet, "/getdummie/dummie/info/refs?service=git-upload-pack", nil)
	r.Host = "llm.int.example.com"
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestServeUnknownPath(t *testing.T) {
	s := testServer(t, minting("ghs_x"), "")
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = githubHost()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("content-type = %q", ct)
	}
}

// git prompts for credentials on a 401, which is the confusing outcome this
// whole feature exists to avoid, so a refusal must never be one.
func TestGitDenialIsNever401(t *testing.T) {
	s := testServer(t, denying(http.StatusForbidden, "intproxy: not attached to this vm"), "")
	r := httptest.NewRequest(http.MethodGet, "/getdummie/dummie/info/refs?service=git-upload-pack", nil)
	r.Host = githubHost()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if !strings.Contains(w.Body.String(), "not attached to this vm") {
		t.Fatalf("body = %q, want the source's reason", w.Body.String())
	}
}

func TestRESTDenialUsesGitHubErrorShape(t *testing.T) {
	s := testServer(t, denying(http.StatusForbidden, "intproxy: not attached"), "")
	r := httptest.NewRequest(http.MethodGet, "/api/v3/repos/getdummie/dummie", nil)
	r.Host = githubHost()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d", w.Code)
	}
	var body struct {
		Message          string `json:"message"`
		DocumentationURL string `json:"documentation_url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.Message, "not attached") {
		t.Fatalf("message = %q", body.Message)
	}
	if body.DocumentationURL != "https://control.example.com/integrations" {
		t.Fatalf("documentation_url = %q", body.DocumentationURL)
	}
}

func TestGraphQLDenialUsesErrorsArray(t *testing.T) {
	s := testServer(t, denying(http.StatusForbidden, "intproxy: not attached"), "")
	r := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(`{"query":"{viewer{login}}"}`))
	r.Host = githubHost()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	var body struct {
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Errors) != 1 || !strings.Contains(body.Errors[0].Message, "not attached") {
		t.Fatalf("errors = %+v", body.Errors)
	}
}

// The scope is the cache key, so anything that changes which token is issued
// has to reach the source.
func TestScopeReachesTheSource(t *testing.T) {
	var seen credential.Scope
	src := fakeSource{fn: func(s credential.Scope) (credential.Token, error) {
		seen = s
		return credential.Token{}, &credential.Denial{Status: http.StatusForbidden, Message: "stop"}
	}}

	s := testServer(t, src, "")
	r := httptest.NewRequest(http.MethodPost, "/getdummie/dummie.git/git-receive-pack", nil)
	r.Host = githubHost()
	r.RemoteAddr = "10.64.0.7:41234"
	s.ServeHTTP(httptest.NewRecorder(), r)

	if seen.Integration != "github" || seen.ClientIP != "10.64.0.7" ||
		seen.Resource != "getdummie/dummie" || !seen.Write {
		t.Fatalf("scope = %+v", seen)
	}
}

func TestPolicyDeniesBeforeAnyCredentialIsFetched(t *testing.T) {
	fetched := false
	src := fakeSource{fn: func(credential.Scope) (credential.Token, error) {
		fetched = true
		return credential.Token{Value: "ghp_x"}, nil
	}}

	s := testServer(t, src, "")
	p := &policy.Policy{Clients: []policy.Client{{
		Name:         "build",
		Source:       "10.64.0.5/32",
		Integrations: []policy.ClientIntegration{{Name: "github", Read: []string{"acme/*"}}},
	}}}
	if err := p.Compile(); err != nil {
		t.Fatal(err)
	}
	s.policy.Store(p)

	r := httptest.NewRequest(http.MethodGet, "/other/repo/info/refs?service=git-upload-pack", nil)
	r.Host = githubHost()
	r.RemoteAddr = "10.64.0.5:2000"
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if fetched {
		t.Fatal("a credential was fetched for a request the policy refused")
	}
}

func TestPolicySelectsTheCredential(t *testing.T) {
	var seen string
	src := fakeSource{fn: func(s credential.Scope) (credential.Token, error) {
		seen = s.Credential
		return credential.Token{}, &credential.Denial{Status: http.StatusForbidden, Message: "stop"}
	}}

	s := testServer(t, src, "")
	p := &policy.Policy{Clients: []policy.Client{{
		Name:         "build",
		Source:       "10.64.0.5/32",
		Integrations: []policy.ClientIntegration{{Name: "github", Credential: "ci", Read: []string{"acme/*"}}},
	}}}
	if err := p.Compile(); err != nil {
		t.Fatal(err)
	}
	s.policy.Store(p)

	r := httptest.NewRequest(http.MethodGet, "/acme/thing/info/refs?service=git-upload-pack", nil)
	r.Host = githubHost()
	r.RemoteAddr = "10.64.0.5:2000"
	s.ServeHTTP(httptest.NewRecorder(), r)

	if seen != "ci" {
		t.Fatalf("credential = %q, want the one the policy picked", seen)
	}
}

func TestGetCertificateAcceptsOnlyServedHosts(t *testing.T) {
	s := testServer(t, minting("ghs_x"), "")
	s.cert.Store(&tls.Certificate{})

	if _, err := s.getCertificate(&tls.ClientHelloInfo{ServerName: "llm.int.example.com"}); err == nil {
		t.Fatal("a foreign SNI was accepted")
	}
	if _, err := s.getCertificate(&tls.ClientHelloInfo{ServerName: githubHost()}); err != nil {
		t.Fatalf("our own SNI was rejected: %v", err)
	}
}
