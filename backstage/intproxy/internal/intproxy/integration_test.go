package intproxy

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// upstreamProbe records what github would have seen.
type upstreamProbe struct {
	auth   string
	path   string
	query  string
	host   string
	proto  string
	cookie string
}

func TestEndToEndCloneInjectsToken(t *testing.T) {
	var seen upstreamProbe
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = upstreamProbe{
			auth:   r.Header.Get("Authorization"),
			path:   r.URL.Path,
			query:  r.URL.RawQuery,
			host:   r.Host,
			proto:  r.Header.Get("Git-Protocol"),
			cookie: r.Header.Get("Cookie"),
		}
		w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		_, _ = w.Write([]byte("001e# service=git-upload-pack\n"))
	}))
	defer upstream.Close()

	s := testServer(t, minting("ghs_minted"), "git_host: "+upstream.Listener.Addr().String())
	s.rp.Transport = upstream.Client().Transport

	r := httptest.NewRequest(http.MethodGet, "/getdummie/dummie.git/info/refs?service=git-upload-pack", nil)
	r.Host = githubHost()
	r.Header.Set("Git-Protocol", "version=2")
	r.Header.Set("Authorization", "Bearer ghp_the_clients_own_token")
	r.Header.Set("Cookie", "session=abc")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "service=git-upload-pack") {
		t.Fatalf("body = %q", w.Body.String())
	}

	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:ghs_minted"))
	if seen.auth != wantAuth {
		t.Fatalf("upstream Authorization = %q, want the minted token", seen.auth)
	}
	if seen.cookie != "" {
		t.Fatal("the client's cookie reached github")
	}
	if seen.path != "/getdummie/dummie.git/info/refs" {
		t.Fatalf("upstream path = %q", seen.path)
	}
	if seen.query != "service=git-upload-pack" {
		t.Fatalf("upstream query = %q", seen.query)
	}
	if seen.proto != "version=2" {
		t.Fatalf("Git-Protocol = %q, want it forwarded", seen.proto)
	}
	if seen.host != upstream.Listener.Addr().String() {
		t.Fatalf("upstream Host = %q", seen.host)
	}
}

func TestEndToEndRESTRewritesPagination(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer ghs_minted" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Link", `<https://`+r.Host+`/user/repos?page=2>; rel="next"`)
		w.Header().Set("X-RateLimit-Remaining", "4999")
		_, _ = io.WriteString(w, `[]`)
	}))
	defer upstream.Close()

	s := testServer(t, minting("ghs_minted"), "api_host: "+upstream.Listener.Addr().String())
	s.rp.Transport = upstream.Client().Transport

	r := httptest.NewRequest(http.MethodGet, "/api/v3/user/repos", nil)
	r.Host = githubHost()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if got := w.Header().Get("X-RateLimit-Remaining"); got != "4999" {
		t.Fatalf("rate limit headers were not passed through: %q", got)
	}
	link := w.Header().Get("Link")
	if want := `<http://github.int.example.com/api/v3/user/repos?page=2>; rel="next"`; link != want {
		t.Fatalf("Link = %q, want %q", link, want)
	}
}

// An upstream 401 is about this proxy's credential, not the caller's. Passed
// through, git would answer it by prompting for a password that cannot help.
func TestUpstream401BecomesOur403(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="GitHub"`)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message":"Bad credentials"}`)
	}))
	defer upstream.Close()

	s := testServer(t, minting("ghp_expired"), "git_host: "+upstream.Listener.Addr().String())
	s.rp.Transport = upstream.Client().Transport

	r := httptest.NewRequest(http.MethodGet, "/acme/thing.git/info/refs?service=git-upload-pack", nil)
	r.Host = githubHost()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; a 401 makes git prompt for credentials", w.Code)
	}
	if w.Header().Get("WWW-Authenticate") != "" {
		t.Fatal("WWW-Authenticate reached the client, so git will prompt for credentials")
	}
	if !strings.Contains(w.Body.String(), "expired or been revoked") {
		t.Fatalf("body = %q, want an explanation of whose credential failed", w.Body.String())
	}
}
