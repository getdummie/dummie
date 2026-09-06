package intproxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func testConfig() *Config {
	return &Config{
		Listen:     "10.64.255.254:443",
		TLD:        "example.com",
		Label:      "int",
		ConsoleURL: "https://control.example.com",
		TLS:        TLSConfig{Cert: "c.pem", Key: "k.pem"},
		Broker:     BrokerConfig{Socket: "/run/intproxy/broker.sock"},
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name   string
		method string
		target string
		want   request
	}{
		{
			name: "clone refs", method: "GET",
			target: "/getdummie/dummie/info/refs?service=git-upload-pack",
			want: request{kind: kindGit, owner: "getdummie", repo: "dummie",
				upstreamHost: "github.com", upstreamPath: "/getdummie/dummie.git/info/refs"},
		},
		{
			name: "clone refs with .git suffix", method: "GET",
			target: "/getdummie/dummie.git/info/refs?service=git-upload-pack",
			want: request{kind: kindGit, owner: "getdummie", repo: "dummie",
				upstreamHost: "github.com", upstreamPath: "/getdummie/dummie.git/info/refs"},
		},
		{
			name: "push refs is a write", method: "GET",
			target: "/getdummie/dummie.git/info/refs?service=git-receive-pack",
			want: request{kind: kindGit, owner: "getdummie", repo: "dummie", write: true,
				upstreamHost: "github.com", upstreamPath: "/getdummie/dummie.git/info/refs"},
		},
		{
			name: "upload-pack", method: "POST",
			target: "/getdummie/dummie.git/git-upload-pack",
			want: request{kind: kindGit, owner: "getdummie", repo: "dummie",
				upstreamHost: "github.com", upstreamPath: "/getdummie/dummie.git/git-upload-pack"},
		},
		{
			name: "receive-pack is a write", method: "POST",
			target: "/getdummie/dummie/git-receive-pack",
			want: request{kind: kindGit, owner: "getdummie", repo: "dummie", write: true,
				upstreamHost: "github.com", upstreamPath: "/getdummie/dummie.git/git-receive-pack"},
		},
		{
			name: "rest with repo", method: "GET",
			target: "/api/v3/repos/getdummie/dummie/issues",
			want: request{kind: kindREST, owner: "getdummie", repo: "dummie",
				upstreamHost: "api.github.com", upstreamPath: "/repos/getdummie/dummie/issues"},
		},
		{
			name: "rest repo root", method: "GET",
			target: "/api/v3/repos/getdummie/dummie",
			want: request{kind: kindREST, owner: "getdummie", repo: "dummie",
				upstreamHost: "api.github.com", upstreamPath: "/repos/getdummie/dummie"},
		},
		{
			name: "rest without repo", method: "GET",
			target: "/api/v3/user/repos",
			want: request{kind: kindREST,
				upstreamHost: "api.github.com", upstreamPath: "/user/repos"},
		},
		{
			name: "rest post is a write", method: "POST",
			target: "/api/v3/repos/getdummie/dummie/issues",
			want: request{kind: kindREST, owner: "getdummie", repo: "dummie", write: true,
				upstreamHost: "api.github.com", upstreamPath: "/repos/getdummie/dummie/issues"},
		},
		{
			name: "graphql", method: "POST", target: "/api/graphql",
			want: request{kind: kindGraphQL, write: true,
				upstreamHost: "api.github.com", upstreamPath: "/graphql"},
		},
		{
			name: "graphql under v3", method: "POST", target: "/api/v3/graphql",
			want: request{kind: kindGraphQL, write: true,
				upstreamHost: "api.github.com", upstreamPath: "/graphql"},
		},
		{name: "bare rest at root is not accepted", method: "GET", target: "/repos/getdummie/dummie"},
		{name: "api prefix only on a segment boundary", method: "GET", target: "/api/v3nope/user"},
		{name: "refs without a service", method: "GET", target: "/getdummie/dummie/info/refs"},
		{name: "refs with an unknown service", method: "GET", target: "/getdummie/dummie/info/refs?service=git-nope"},
		{name: "root", method: "GET", target: "/"},
		{name: "unrelated path", method: "GET", target: "/getdummie/dummie"},
		{name: "traversal in owner", method: "GET", target: "/..%2F..%2Fetc/dummie/info/refs?service=git-upload-pack"},
	}

	cfg := testConfig()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.target, nil)
			got := classify(cfg, r)
			if got != tc.want {
				t.Fatalf("classify(%s %s)\n got %+v\nwant %+v", tc.method, tc.target, got, tc.want)
			}
		})
	}
}

func TestClassifyRejectsTraversal(t *testing.T) {
	cfg := testConfig()
	for _, target := range []string{
		"/a/../../etc/info/refs?service=git-upload-pack",
		"/./x/info/refs?service=git-upload-pack",
		"/-bad/repo/info/refs?service=git-upload-pack",
	} {
		r := httptest.NewRequest(http.MethodGet, target, nil)
		if got := classify(cfg, r); got.kind != kindUnknown {
			t.Fatalf("classify(%s) = %+v, want unknown", target, got)
		}
	}
}
