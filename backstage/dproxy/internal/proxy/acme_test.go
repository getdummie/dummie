package proxy

import "testing"

func acmeProxy(target string) *Proxy {
	cfg := &Config{}
	if target != "" {
		cfg.ACME = &ACMEConfig{ChallengeTarget: target}
	}
	return &Proxy{cfg: cfg}
}

func TestACMETargetClaimsOnlyTheChallengePath(t *testing.T) {
	p := acmeProxy("127.0.0.1:8078")

	for _, tc := range []struct {
		name    string
		request string
		want    bool
	}{
		{"the challenge", "GET /.well-known/acme-challenge/tok HTTP/1.1\r\nHost: codingcoffee.dev\r\n\r\n", true},
		{"the guest's own well-known", "GET /.well-known/other HTTP/1.1\r\nHost: codingcoffee.dev\r\n\r\n", false},
		{"the root", "GET / HTTP/1.1\r\nHost: codingcoffee.dev\r\n\r\n", false},
		{"a path that only looks like it", "GET /x/.well-known/acme-challenge/tok HTTP/1.1\r\nHost: x\r\n\r\n", false},
		{"garbage", "not a request line\r\n\r\n", false},
	} {
		if _, got := p.acmeTarget([]byte(tc.request)); got != tc.want {
			t.Errorf("%s: claimed=%v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestACMETargetIsInertWhenUnconfigured(t *testing.T) {
	p := acmeProxy("")
	req := []byte("GET /.well-known/acme-challenge/tok HTTP/1.1\r\nHost: codingcoffee.dev\r\n\r\n")

	if _, ok := p.acmeTarget(req); ok {
		t.Error("the challenge path was claimed with no acme block; the guest would lose that path for nothing")
	}
}
