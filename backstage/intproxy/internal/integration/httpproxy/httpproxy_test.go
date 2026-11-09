package httpproxy

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/netip"
	"testing"

	"gopkg.in/yaml.v3"

	"intproxy/internal/credential"
	"intproxy/internal/integration"
)

func testProxy(t *testing.T) *HTTPProxy {
	t.Helper()
	ig, err := New(integration.Options{
		Name:      "u",
		Transport: &http.Transport{},
		Decode:    func(v any) error { return yaml.Unmarshal([]byte(`deny: ["10.64.0.0/16"]`), v) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return ig.(*HTTPProxy)
}

func TestClassifyReadsTheProxyOffTheHost(t *testing.T) {
	p := testProxy(t)
	r := httptest.NewRequest(http.MethodPost, "/a/../../etc/x/?q=1", nil)
	r.Host = "jira-u.int.example.com:443"
	route, ok := p.Classify(r)
	if !ok {
		t.Fatal("refused")
	}
	if route.Resource != "jira" || !route.Write || !route.Generic {
		t.Fatalf("route = %+v", route)
	}
	if route.UpstreamPath != "/etc/x/" {
		t.Fatalf("path = %q, want it cleaned under the base", route.UpstreamPath)
	}
}

func TestClassifyRefusesABareSuffix(t *testing.T) {
	p := testProxy(t)
	for _, host := range []string{"-u.int.example.com", "u.int.example.com", "jira.int.example.com"} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Host = host
		if _, ok := p.Classify(r); ok {
			t.Errorf("%s was accepted", host)
		}
	}
}

func TestApplySetsTheConfiguredHeaders(t *testing.T) {
	p := testProxy(t)
	out := httptest.NewRequest(http.MethodGet, "/", nil)
	out.Header.Set("X-Token", "theirs")
	p.Apply(&httputil.ProxyRequest{Out: out}, integration.Route{}, credential.Token{
		Headers: map[string]string{"X-Token": "ours", "Authorization": "Bearer t"},
	})
	if out.Header.Get("X-Token") != "ours" || out.Header.Get("Authorization") != "Bearer t" {
		t.Fatalf("headers = %v", out.Header)
	}
}

func TestGuardRefusesTheHostAndThePool(t *testing.T) {
	g := guard{deny: []netip.Prefix{netip.MustParsePrefix("10.64.0.0/16")}}
	for _, addr := range []string{
		"127.0.0.1:80", "[::1]:443", "169.254.169.254:80", "0.0.0.0:80",
		"[::ffff:127.0.0.1]:80", "10.64.255.254:443", "10.64.0.7:22",
	} {
		if g.control("tcp", addr, nil) == nil {
			t.Errorf("%s was allowed", addr)
		}
	}
	if err := g.control("tcp", "192.0.2.10:443", nil); err != nil {
		t.Errorf("an address that is not the host's own was refused: %v", err)
	}
}

func TestInsecureOnlyWhenAsked(t *testing.T) {
	p := testProxy(t)
	if p.RoundTripper(credential.Token{}) != p.secure || p.RoundTripper(credential.Token{InsecureTLS: true}) != p.insecure {
		t.Fatal("the wrong transport was picked")
	}
	if c := p.secure.(*http.Transport).TLSClientConfig; c != nil && c.InsecureSkipVerify {
		t.Fatal("the verifying transport skips verification")
	}
}
