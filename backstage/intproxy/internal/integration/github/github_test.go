package github

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"

	"intproxy/internal/credential"
	"intproxy/internal/integration"
)

func testGitHub(t *testing.T) *GitHub {
	t.Helper()
	ig, err := New(integration.Options{
		DocsURL: "https://control.example.com/integrations",
		SelfURL: func() string { return "https://github.int.example.com" },
		Version: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return ig.(*GitHub)
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name   string
		method string
		target string
		want   integration.Route
		ok     bool
	}{
		{
			name: "clone refs", method: "GET",
			target: "/getdummie/dummie/info/refs?service=git-upload-pack",
			want: integration.Route{Kind: kindGit, Resource: "getdummie/dummie",
				UpstreamHost: "github.com", UpstreamPath: "/getdummie/dummie.git/info/refs"},
			ok: true,
		},
		{
			name: "clone refs with .git suffix", method: "GET",
			target: "/getdummie/dummie.git/info/refs?service=git-upload-pack",
			want: integration.Route{Kind: kindGit, Resource: "getdummie/dummie",
				UpstreamHost: "github.com", UpstreamPath: "/getdummie/dummie.git/info/refs"},
			ok: true,
		},
		{
			name: "push refs is a write", method: "GET",
			target: "/getdummie/dummie.git/info/refs?service=git-receive-pack",
			want: integration.Route{Kind: kindGit, Resource: "getdummie/dummie", Write: true,
				UpstreamHost: "github.com", UpstreamPath: "/getdummie/dummie.git/info/refs"},
			ok: true,
		},
		{
			name: "upload-pack", method: "POST",
			target: "/getdummie/dummie.git/git-upload-pack",
			want: integration.Route{Kind: kindGit, Resource: "getdummie/dummie",
				UpstreamHost: "github.com", UpstreamPath: "/getdummie/dummie.git/git-upload-pack"},
			ok: true,
		},
		{
			name: "receive-pack is a write", method: "POST",
			target: "/getdummie/dummie/git-receive-pack",
			want: integration.Route{Kind: kindGit, Resource: "getdummie/dummie", Write: true,
				UpstreamHost: "github.com", UpstreamPath: "/getdummie/dummie.git/git-receive-pack"},
			ok: true,
		},
		{
			name: "rest with repo", method: "GET",
			target: "/api/v3/repos/getdummie/dummie/issues",
			want: integration.Route{Kind: kindREST, Resource: "getdummie/dummie",
				UpstreamHost: "api.github.com", UpstreamPath: "/repos/getdummie/dummie/issues"},
			ok: true,
		},
		{
			name: "rest repo root", method: "GET",
			target: "/api/v3/repos/getdummie/dummie",
			want: integration.Route{Kind: kindREST, Resource: "getdummie/dummie",
				UpstreamHost: "api.github.com", UpstreamPath: "/repos/getdummie/dummie"},
			ok: true,
		},
		{
			name: "rest without repo", method: "GET",
			target: "/api/v3/user/repos",
			want: integration.Route{Kind: kindREST,
				UpstreamHost: "api.github.com", UpstreamPath: "/user/repos"},
			ok: true,
		},
		{
			name: "rest post is a write", method: "POST",
			target: "/api/v3/repos/getdummie/dummie/issues",
			want: integration.Route{Kind: kindREST, Resource: "getdummie/dummie", Write: true,
				UpstreamHost: "api.github.com", UpstreamPath: "/repos/getdummie/dummie/issues"},
			ok: true,
		},
		{
			name: "graphql", method: "POST", target: "/api/graphql",
			want: integration.Route{Kind: kindGraphQL, Write: true,
				UpstreamHost: "api.github.com", UpstreamPath: "/graphql"},
			ok: true,
		},
		{
			name: "graphql under v3", method: "POST", target: "/api/v3/graphql",
			want: integration.Route{Kind: kindGraphQL, Write: true,
				UpstreamHost: "api.github.com", UpstreamPath: "/graphql"},
			ok: true,
		},
		{name: "bare rest at root is not accepted", method: "GET", target: "/repos/getdummie/dummie"},
		{name: "api prefix only on a segment boundary", method: "GET", target: "/api/v3nope/user"},
		{name: "refs without a service", method: "GET", target: "/getdummie/dummie/info/refs"},
		{name: "refs with an unknown service", method: "GET", target: "/getdummie/dummie/info/refs?service=git-nope"},
		{name: "root", method: "GET", target: "/"},
		{name: "unrelated path", method: "GET", target: "/getdummie/dummie"},
		{name: "traversal in owner", method: "GET", target: "/..%2F..%2Fetc/dummie/info/refs?service=git-upload-pack"},
	}

	g := testGitHub(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.target, nil)
			got, ok := g.Classify(r)
			if ok != tc.ok {
				t.Fatalf("Classify(%s %s) ok = %v, want %v", tc.method, tc.target, ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Fatalf("Classify(%s %s)\n got %+v\nwant %+v", tc.method, tc.target, got, tc.want)
			}
		})
	}
}

// Owner and repo names are rejected rather than cleaned. That is the
// path-traversal guard.
func TestClassifyRejectsTraversal(t *testing.T) {
	g := testGitHub(t)
	for _, target := range []string{
		"/a/../../etc/info/refs?service=git-upload-pack",
		"/./x/info/refs?service=git-upload-pack",
		"/-bad/repo/info/refs?service=git-upload-pack",
	} {
		r := httptest.NewRequest(http.MethodGet, target, nil)
		if _, ok := g.Classify(r); ok {
			t.Fatalf("Classify(%s) was accepted, want a refusal", target)
		}
	}
}

func applied(t *testing.T, g *GitHub, in *http.Request, route integration.Route, token string) *http.Request {
	t.Helper()
	out := in.Clone(in.Context())
	out.URL = &url.URL{}
	g.Apply(&httputil.ProxyRequest{In: in, Out: out}, route, credential.Token{Value: token})
	return out
}

func TestApplyUsesBasicForGit(t *testing.T) {
	g := testGitHub(t)
	in := httptest.NewRequest(http.MethodGet, "/getdummie/dummie/info/refs?service=git-upload-pack", nil)
	route, _ := g.Classify(in)
	out := applied(t, g, in, route, "ghs_minted")

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:ghs_minted"))
	if got := out.Header.Get("Authorization"); got != want {
		t.Fatalf("Authorization = %q, want the token as basic auth", got)
	}
}

func TestApplyUsesBearerForTheAPI(t *testing.T) {
	g := testGitHub(t)
	in := httptest.NewRequest(http.MethodGet, "/api/v3/repos/getdummie/dummie", nil)
	route, _ := g.Classify(in)
	out := applied(t, g, in, route, "ghs_minted")

	if got := out.Header.Get("Authorization"); got != "Bearer ghs_minted" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := out.Header.Get("X-GitHub-Api-Version"); got != "2022-11-28" {
		t.Fatalf("X-GitHub-Api-Version = %q", got)
	}
}

func TestModifyResponseRewritesLinkAndLocation(t *testing.T) {
	g := testGitHub(t)
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Link", `<https://api.github.com/user/repos?page=2>; rel="next", <https://api.github.com/user/repos?page=9>; rel="last"`)
	resp.Header.Set("Location", "https://github.com/getdummie/renamed.git")
	resp.Header.Set("Set-Cookie", "logged_in=no")

	if err := g.ModifyResponse(resp); err != nil {
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

func TestNewReadsItsOwnOptions(t *testing.T) {
	ig, err := New(integration.Options{
		Decode: func(v any) error {
			c, ok := v.(*Config)
			if !ok {
				t.Fatalf("decoded into %T", v)
			}
			c.GitHost = "ghe.example.com"
			c.APIHost = "ghe.example.com/api/v3"
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	g := ig.(*GitHub)
	if g.gitHost != "ghe.example.com" || g.apiHost != "ghe.example.com/api/v3" {
		t.Fatalf("hosts = %s / %s", g.gitHost, g.apiHost)
	}
}
