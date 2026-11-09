package intproxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"intproxy/internal/credential"
	"intproxy/internal/integration"
)

func withUserProxies(t *testing.T, s *Server) {
	t.Helper()
	ig, err := integration.New("http", integration.Options{Name: "u", Transport: s.newTransport(), DialTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	s.families = map[string]integration.Integration{"-u": ig}
}

func userProxy(target string, methods ...string) fakeSource {
	return fakeSource{fn: func(sc credential.Scope) (credential.Token, error) {
		if sc.Integration != "u" || sc.Resource != "jira" {
			return credential.Token{}, &credential.Denial{Status: http.StatusForbidden, Message: "wrong scope " + sc.Resource}
		}
		return credential.Token{Upstream: target, Methods: methods, ExpiresAt: time.Now().Add(time.Hour)}, nil
	}}
}

func TestUserProxyRefusesAMethodItDoesNotAllow(t *testing.T) {
	s := testServer(t, userProxy("https://example.org", "GET"), "")
	withUserProxies(t, s)

	r := httptest.NewRequest(http.MethodDelete, "/x", nil)
	r.Host = "jira-u.int.example.com"
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET" {
		t.Fatalf("status = %d, allow %q", w.Code, w.Header().Get("Allow"))
	}
}

// The upstream is loopback, which the dial guard refuses, so reaching the guard
// at all proves the family routed it.
func TestUserProxyCannotDialTheHost(t *testing.T) {
	hit := false
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer upstream.Close()

	s := testServer(t, userProxy(upstream.URL, "GET"), "")
	withUserProxies(t, s)

	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.Host = "jira-u.int.example.com"
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	if hit || w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, upstream hit %t", w.Code, hit)
	}
}

func TestBuiltInNamesWinOverTheFamily(t *testing.T) {
	s := testServer(t, minting("t"), "")
	withUserProxies(t, s)
	if ig, ok := s.lookup(githubHost()); !ok || ig.Name() != "github" {
		t.Fatal("github was not routed to github")
	}
	for _, host := range []string{"u.int.example.com", "-u.int.example.com", "a.b-u.int.example.com", "jira-u.other.com"} {
		if _, ok := s.lookup(host); ok {
			t.Errorf("%s was routed", host)
		}
	}
	if ig, ok := s.lookup("jira-u.int.example.com"); !ok || !strings.EqualFold(ig.Name(), "u") {
		t.Fatal("a user proxy was not routed")
	}
}
