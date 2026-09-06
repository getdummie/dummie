package intproxy

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

	b := fakeBroker(t, func(w http.ResponseWriter, r *http.Request) {
		var tr tokenRequest
		_ = json.NewDecoder(r.Body).Decode(&tr)
		if tr.Repo != "getdummie/dummie" || tr.VMIP != "192.0.2.1" {
			t.Errorf("broker saw %+v", tr)
		}
		_ = json.NewEncoder(w).Encode(tokenResponse{Token: "ghs_minted", ExpiresAt: time.Now().Add(time.Hour)})
	})

	s := serverWithBroker(t, b)
	s.cfg.GitHub.GitHost = upstream.Listener.Addr().String()
	s.rp = newReverseProxy(s)
	s.rp.rp.Transport = upstream.Client().Transport

	r := httptest.NewRequest(http.MethodGet, "/getdummie/dummie.git/info/refs?service=git-upload-pack", nil)
	r.Host = s.ServerName()
	r.Header.Set("Git-Protocol", "version=2")
	r.Header.Set("Authorization", "Bearer ghp_the_guests_own_token")
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
		t.Fatal("the guest's cookie reached github")
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
	if seen.host != s.cfg.GitHub.GitHost {
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

	b := fakeBroker(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(tokenResponse{Token: "ghs_minted", ExpiresAt: time.Now().Add(time.Hour)})
	})

	s := serverWithBroker(t, b)
	s.cfg.GitHub.APIHost = upstream.Listener.Addr().String()
	s.rp = newReverseProxy(s)
	s.rp.rp.Transport = upstream.Client().Transport

	r := httptest.NewRequest(http.MethodGet, "/api/v3/user/repos", nil)
	r.Host = s.ServerName()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if got := w.Header().Get("X-RateLimit-Remaining"); got != "4999" {
		t.Fatalf("rate limit headers were not passed through: %q", got)
	}
	link := w.Header().Get("Link")
	if want := `<https://github.int.example.com/api/v3/user/repos?page=2>; rel="next"`; link != want {
		t.Fatalf("Link = %q, want %q", link, want)
	}
}
