package main

import (
	"slices"
	"testing"
)

func TestNormalizeProxyTarget(t *testing.T) {
	for raw, want := range map[string]string{
		"https://api.example.com/v1/": "https://api.example.com/v1",
		"http://10.0.0.5:8080":        "http://10.0.0.5:8080",
	} {
		if got, err := normalizeProxyTarget(raw); err != nil || got != want {
			t.Errorf("%q = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"ftp://x", "https://", "https://u:p@x", "https://x/?a=1", "https://x/#f", "x.com", "https://:443"} {
		if _, err := normalizeProxyTarget(raw); err == nil {
			t.Errorf("%q was accepted", raw)
		}
	}
}

func TestNormalizeProxyMethods(t *testing.T) {
	got, err := normalizeProxyMethods([]string{"delete", "GET", "get"})
	if err != nil || !slices.Equal(got, []string{"GET", "DELETE"}) {
		t.Fatalf("got %v, %v", got, err)
	}
	for _, bad := range [][]string{nil, {"TRACE"}, {"CONNECT"}} {
		if _, err := normalizeProxyMethods(bad); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
}

func TestMergeProxyHeadersKeepsWriteOnlyValues(t *testing.T) {
	v := "new"
	got, err := mergeProxyHeaders(
		[]httpProxyHeader{{Name: "authorization"}, {Name: "x-team", Value: &v}},
		[]storedHeader{{Name: "Authorization", Value: "Bearer old"}, {Name: "X-Dropped", Value: "x"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []storedHeader{{Name: "Authorization", Value: "Bearer old"}, {Name: "X-Team", Value: "new"}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMergeProxyHeadersRefuses(t *testing.T) {
	v, crlf := "x", "a\r\nX-Evil: 1"
	for name, req := range map[string][]httpProxyHeader{
		"framing":     {{Name: "Host", Value: &v}},
		"hop-by-hop":  {{Name: "connection", Value: &v}},
		"bad name":    {{Name: "x y", Value: &v}},
		"duplicate":   {{Name: "X-A", Value: &v}, {Name: "x-a", Value: &v}},
		"no value":    {{Name: "X-New"}},
		"header split": {{Name: "X-A", Value: &crlf}},
	} {
		if _, err := mergeProxyHeaders(req, nil); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestGitLabNamesStayClearOfUserProxies(t *testing.T) {
	for _, name := range []string{"u", "corp-u"} {
		if validateGitLabName(name) == nil {
			t.Errorf("%q was accepted", name)
		}
	}
}
