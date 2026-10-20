package gitlab

import (
	"encoding/base64"
	"net/http/httptest"
	"net/http/httputil"
	"testing"

	"intproxy/internal/credential"
	"intproxy/internal/integration"
)

func testGitLab(t *testing.T) *GitLab {
	t.Helper()
	ig, err := New(integration.Options{Name: "techgitlab", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return ig.(*GitLab)
}

func TestNameIsTheConfiguredOne(t *testing.T) {
	if got := testGitLab(t).Name(); got != "techgitlab" {
		t.Fatalf("name = %q", got)
	}
	if _, err := New(integration.Options{}); err == nil {
		t.Fatal("a gitlab without a name was accepted")
	}
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
			target: "/group/proj/info/refs?service=git-upload-pack",
			want:   integration.Route{Kind: kindGit, Resource: "group/proj", UpstreamPath: "/group/proj.git/info/refs"},
			ok:     true,
		},
		{
			name: "nested groups with .git", method: "GET",
			target: "/a/b.c/d_e/proj.git/info/refs?service=git-upload-pack",
			want:   integration.Route{Kind: kindGit, Resource: "a/b.c/d_e/proj", UpstreamPath: "/a/b.c/d_e/proj.git/info/refs"},
			ok:     true,
		},
		{
			name: "push refs is a write", method: "GET",
			target: "/group/proj.git/info/refs?service=git-receive-pack",
			want:   integration.Route{Kind: kindGit, Resource: "group/proj", Write: true, UpstreamPath: "/group/proj.git/info/refs"},
			ok:     true,
		},
		{
			name: "upload-pack", method: "POST",
			target: "/group/sub/proj.git/git-upload-pack",
			want:   integration.Route{Kind: kindGit, Resource: "group/sub/proj", UpstreamPath: "/group/sub/proj.git/git-upload-pack"},
			ok:     true,
		},
		{
			name: "receive-pack is a write", method: "POST",
			target: "/group/proj/git-receive-pack",
			want:   integration.Route{Kind: kindGit, Resource: "group/proj", Write: true, UpstreamPath: "/group/proj.git/git-receive-pack"},
			ok:     true,
		},
		{name: "no namespace", method: "GET", target: "/proj.git/info/refs?service=git-upload-pack"},
		{name: "dumb http", method: "GET", target: "/group/proj.git/info/refs"},
		{name: "api", method: "GET", target: "/api/v4/projects"},
		{name: "traversal", method: "GET", target: "/group/../proj.git/info/refs?service=git-upload-pack"},
		{name: "dot dot inside", method: "GET", target: "/group/a..b/info/refs?service=git-upload-pack"},
		{name: "empty segment", method: "GET", target: "/group//proj/info/refs?service=git-upload-pack"},
		{name: "hidden segment", method: "GET", target: "/.group/proj/info/refs?service=git-upload-pack"},
	}

	g := testGitLab(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := g.Classify(httptest.NewRequest(tc.method, tc.target, nil))
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (route %+v)", ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Fatalf("route = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestApplyUsesBasicWithOAuth2User(t *testing.T) {
	g := testGitLab(t)
	in := httptest.NewRequest("GET", "/group/proj.git/info/refs", nil)
	out := in.Clone(in.Context())
	g.Apply(&httputil.ProxyRequest{In: in, Out: out}, integration.Route{Kind: kindGit}, credential.Token{Value: "glpat-x"})

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("oauth2:glpat-x"))
	if got := out.Header.Get("Authorization"); got != want {
		t.Fatalf("authorization = %q, want %q", got, want)
	}
}
