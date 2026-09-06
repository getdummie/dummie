package intproxy

import (
	"context"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	s := &Server{cfg: testConfig(), log: slog.New(slog.DiscardHandler), version: "test"}
	s.broker = newBroker(s.cfg)
	s.rp = newReverseProxy(s)
	return s
}

func rewriteFor(t *testing.T, s *Server, in *http.Request, req request, token string) *http.Request {
	t.Helper()
	ctx := context.WithValue(in.Context(), ctxRequest, req)
	ctx = context.WithValue(ctx, ctxToken, token)
	in = in.WithContext(ctx)

	out := in.Clone(in.Context())
	out.URL = &url.URL{}
	pr := &httputil.ProxyRequest{In: in, Out: out}
	s.rp.rewrite(pr)
	return pr.Out
}

func TestRewriteStripsClientCredentials(t *testing.T) {
	s := testServer(t)
	in := httptest.NewRequest(http.MethodGet, "/getdummie/dummie/info/refs?service=git-upload-pack", nil)
	in.Header.Set("Authorization", "Bearer ghp_a_real_user_token")
	in.Header.Set("Cookie", "session=abc")
	in.Header.Set("X-Forwarded-For", "10.64.0.7")

	req := classify(s.cfg, in)
	out := rewriteFor(t, s, in, req, "ghs_minted")

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:ghs_minted"))
	if got := out.Header.Get("Authorization"); got != want {
		t.Fatalf("Authorization = %q, want the minted token", got)
	}
	for _, h := range []string{"Cookie", "X-Forwarded-For", "X-Real-Ip", "Forwarded"} {
		if v := out.Header.Get(h); v != "" {
			t.Fatalf("%s survived the rewrite as %q", h, v)
		}
	}
	if out.URL.Host != "github.com" || out.Host != "github.com" {
		t.Fatalf("upstream = %s / %s, want github.com", out.URL.Host, out.Host)
	}
	if out.URL.Path != "/getdummie/dummie.git/info/refs" {
		t.Fatalf("path = %q", out.URL.Path)
	}
	if !strings.Contains(out.URL.RawQuery, "service=git-upload-pack") {
		t.Fatalf("query = %q, want the service parameter forwarded", out.URL.RawQuery)
	}
}

func TestRewriteAPIUsesBearer(t *testing.T) {
	s := testServer(t)
	in := httptest.NewRequest(http.MethodGet, "/api/v3/repos/getdummie/dummie", nil)
	req := classify(s.cfg, in)
	out := rewriteFor(t, s, in, req, "ghs_minted")

	if got := out.Header.Get("Authorization"); got != "Bearer ghs_minted" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := out.Header.Get("X-GitHub-Api-Version"); got != "2022-11-28" {
		t.Fatalf("X-GitHub-Api-Version = %q", got)
	}
	if out.URL.Host != "api.github.com" || out.URL.Path != "/repos/getdummie/dummie" {
		t.Fatalf("upstream = %s%s", out.URL.Host, out.URL.Path)
	}
}

func TestModifyResponseRewritesLinkAndLocation(t *testing.T) {
	s := testServer(t)
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Link", `<https://api.github.com/user/repos?page=2>; rel="next", <https://api.github.com/user/repos?page=9>; rel="last"`)
	resp.Header.Set("Location", "https://github.com/getdummie/renamed.git")
	resp.Header.Set("Set-Cookie", "logged_in=no")

	if err := s.rp.modifyResponse(resp); err != nil {
		t.Fatal(err)
	}

	link := resp.Header.Get("Link")
	if strings.Contains(link, "api.github.com") {
		t.Fatalf("Link still points upstream: %s", link)
	}
	if !strings.Contains(link, "https://github.int.example.com/api/v3/user/repos?page=2") {
		t.Fatalf("Link = %s", link)
	}
	if !strings.Contains(link, `rel="last"`) {
		t.Fatalf("Link lost a relation: %s", link)
	}
	if got := resp.Header.Get("Location"); got != "https://github.int.example.com/getdummie/renamed.git" {
		t.Fatalf("Location = %q", got)
	}
	if resp.Header.Get("Set-Cookie") != "" {
		t.Fatal("Set-Cookie survived")
	}
}
