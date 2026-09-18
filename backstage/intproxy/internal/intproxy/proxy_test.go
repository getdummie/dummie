package intproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"

	"intproxy/internal/credential"
	"intproxy/internal/integration"
)

func rewritten(t *testing.T, s *Server, in *http.Request) *http.Request {
	t.Helper()
	ig := s.hosts[githubHost()]
	route, ok := ig.Classify(in)
	if !ok {
		t.Fatalf("the test request was not classified: %s", in.URL)
	}

	ctx := context.WithValue(in.Context(), ctxIntegration, ig)
	ctx = context.WithValue(ctx, ctxRoute, route)
	ctx = context.WithValue(ctx, ctxToken, credential.Token{Value: "ghs_minted"})
	in = in.WithContext(ctx)

	out := in.Clone(in.Context())
	out.URL = &url.URL{}
	pr := &httputil.ProxyRequest{In: in, Out: out}
	s.rewrite(pr)
	return pr.Out
}

// A client may well hold a real token. Forwarding it would be an exfiltration
// path, and honouring it would bypass the authorization model entirely.
func TestRewriteStripsClientCredentials(t *testing.T) {
	s := testServer(t, minting("ghs_minted"), "")
	in := httptest.NewRequest(http.MethodGet, "/getdummie/dummie/info/refs?service=git-upload-pack", nil)
	in.Header.Set("Authorization", "Bearer ghp_a_real_user_token")
	in.Header.Set("Cookie", "session=abc")
	in.Header.Set("X-Forwarded-For", "10.64.0.7")
	in.Header.Set("X-Real-Ip", "10.64.0.7")
	in.Header.Set("Git-Protocol", "version=2")

	out := rewritten(t, s, in)

	if got := out.Header.Get("Authorization"); strings.Contains(got, "ghp_a_real_user_token") {
		t.Fatalf("the client's own token survived as %q", got)
	}
	for _, h := range []string{"Cookie", "X-Forwarded-For", "X-Real-Ip", "Forwarded"} {
		if v := out.Header.Get(h); v != "" {
			t.Fatalf("%s survived the rewrite as %q", h, v)
		}
	}
	// git's v2 protocol negotiation needs this one.
	if out.Header.Get("Git-Protocol") != "version=2" {
		t.Fatal("Git-Protocol was not forwarded")
	}
}

func TestRewriteTargetsTheUpstream(t *testing.T) {
	s := testServer(t, minting("ghs_minted"), "")
	in := httptest.NewRequest(http.MethodGet, "/getdummie/dummie/info/refs?service=git-upload-pack", nil)
	out := rewritten(t, s, in)

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

func TestReplaceWithErrorDropsEveryUpstreamHeader(t *testing.T) {
	s := testServer(t, minting("ghs_minted"), "")
	ig := s.hosts[githubHost()]

	resp := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header:     http.Header{},
		Body:       http.NoBody,
	}
	resp.Header.Set("WWW-Authenticate", `Basic realm="GitHub"`)
	resp.Header.Set("X-Github-Request-Id", "abc")

	if err := replaceWithError(resp, ig, integration.Route{Kind: "git"}, http.StatusForbidden, "intproxy: nope"); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if resp.Header.Get("WWW-Authenticate") != "" || resp.Header.Get("X-Github-Request-Id") != "" {
		t.Fatalf("upstream headers survived: %v", resp.Header)
	}
	if resp.ContentLength != int64(len("intproxy: nope\n")) {
		t.Fatalf("content length = %d", resp.ContentLength)
	}
}
