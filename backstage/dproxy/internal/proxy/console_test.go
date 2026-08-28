package proxy

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestConsoleVMHost(t *testing.T) {
	cfg := &Config{HTTP: &HTTPConfig{Hosts: map[string]HTTPHost{
		"one.vm.local": {Host: "10.64.0.2", DefaultPort: 8000},
		"two.shell.vm.local": {Host: "10.64.0.3", DefaultPort: 8000},
	}}}
	p := &Proxy{cfg: cfg, router: NewRouter(cfg)}

	cases := []struct {
		name string
		host string
		want string
	}{
		{"console name", "one.shell.vm.local", "one.vm.local"},
		{"case and port are normalised", "ONE.shell.vm.local:80", "one.vm.local"},
		{"unknown vm", "nope.shell.vm.local", ""},
		{"not a console name", "one.vm.local", ""},
		{"wrong label position", "shell.one.vm.local", ""},
		{"too few labels", "shell.local", ""},
	}
	for _, c := range cases {
		p.cfg.Console = &ConsoleConfig{}
		got, ok := p.consoleVMHost(c.host)
		if !ok {
			got = ""
		}
		if got != c.want {
			t.Errorf("%s: consoleVMHost(%q) = %q, want %q", c.name, c.host, got, c.want)
		}
	}

	p.cfg.Console = nil
	if _, ok := p.consoleVMHost("one.shell.vm.local"); ok {
		t.Error("a console name resolved with no console: configured")
	}
}

func mintConsoleToken(a *Authenticator, sub, host string) string {
	payload, err := json.Marshal(claims{
		Sub: sub,
		Aud: consoleAudPrefix + host,
		Exp: a.now().Add(time.Minute).Unix(),
	})
	if err != nil {
		panic(err)
	}
	b := base64.RawURLEncoding.EncodeToString(payload)
	return b + "." + base64.RawURLEncoding.EncodeToString(a.sign([]byte(b)))
}

func TestConsoleAndSessionTokensAreNotInterchangeable(t *testing.T) {
	a := newTestAuth(t)
	session := a.Mint("cc@example.com", "one.vm.local")
	console := mintConsoleToken(a, "cc@example.com", "one.vm.local")

	if _, ok := a.VerifyConsole(session, "one.vm.local"); ok {
		t.Error("a bare-hostname aud opened a console")
	}
	if _, ok := a.Verify(console, "one.vm.local"); ok {
		t.Error("a console aud passed as an HTTP session")
	}
	if sub, ok := a.VerifyConsole(console, "one.vm.local"); !ok || sub != "cc@example.com" {
		t.Errorf("VerifyConsole = (%q, %v)", sub, ok)
	}
	if _, ok := a.Verify(session, "one.vm.local"); !ok {
		t.Error("the session token stopped working")
	}
}

func TestVerifyConsoleRejects(t *testing.T) {
	a := newTestAuth(t)
	tok := mintConsoleToken(a, "cc@example.com", "one.vm.local")

	if _, ok := a.VerifyConsole(tok, "two.vm.local"); ok {
		t.Error("console token replayed across hosts")
	}
	if _, ok := a.VerifyConsole(tok[:len(tok)-1]+"x", "one.vm.local"); ok {
		t.Error("tampered signature accepted")
	}
	if _, ok := a.VerifyConsole("garbage", "one.vm.local"); ok {
		t.Error("malformed token accepted")
	}
	if _, ok := a.VerifyConsole(tok, ""); ok {
		t.Error("empty host accepted")
	}
	if _, ok := a.Verify(tok, "console:one.vm.local"); ok {
		t.Error("a console audience was accepted as a session audience")
	}
}

func TestWebsocketKeyValidation(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	base := map[string]string{
		"Upgrade":               "websocket",
		"Connection":            "keep-alive, Upgrade",
		"Sec-WebSocket-Version": "13",
		"Sec-WebSocket-Key":     key,
	}
	build := func(mutate func(map[string]string)) []byte {
		h := map[string]string{}
		for k, v := range base {
			h[k] = v
		}
		mutate(h)
		var b strings.Builder
		b.WriteString("GET /?token=t HTTP/1.1\r\nHost: one.shell.vm.local\r\n")
		for k, v := range h {
			if v != "" {
				b.WriteString(k + ": " + v + "\r\n")
			}
		}
		b.WriteString("\r\n")
		return []byte(b.String())
	}

	req, err := parseRequest(build(func(map[string]string) {}))
	if err != nil {
		t.Fatalf("parseRequest: %v", err)
	}
	if got, ok := websocketKey(req); !ok || got != key {
		t.Fatalf("websocketKey = (%q, %v)", got, ok)
	}

	bad := []struct {
		name   string
		mutate func(map[string]string)
	}{
		{"no upgrade header", func(h map[string]string) { h["Upgrade"] = "" }},
		{"wrong upgrade", func(h map[string]string) { h["Upgrade"] = "h2c" }},
		{"no connection upgrade", func(h map[string]string) { h["Connection"] = "keep-alive" }},
		{"wrong version", func(h map[string]string) { h["Sec-WebSocket-Version"] = "8" }},
		{"no key", func(h map[string]string) { h["Sec-WebSocket-Key"] = "" }},
		{"key not base64", func(h map[string]string) { h["Sec-WebSocket-Key"] = "!!!" }},
		{"key not 16 bytes", func(h map[string]string) { h["Sec-WebSocket-Key"] = "c2hvcnQ=" }},
	}
	for _, c := range bad {
		req, err := parseRequest(build(c.mutate))
		if err != nil {
			t.Fatalf("%s: parseRequest: %v", c.name, err)
		}
		if _, ok := websocketKey(req); ok {
			t.Errorf("%s: accepted", c.name)
		}
	}
}

func TestPipelinedBytes(t *testing.T) {
	if got := pipelinedBytes([]byte("GET / HTTP/1.1\r\n\r\nextra")); string(got) != "extra" {
		t.Errorf("pipelinedBytes = %q", got)
	}
	if got := pipelinedBytes([]byte("GET / HTTP/1.1\r\n\r\n")); len(got) != 0 {
		t.Errorf("pipelinedBytes = %q, want empty", got)
	}
}

func TestConsoleConfigValidation(t *testing.T) {
	base := func() *Config {
		return &Config{
			ControlSocket: "/run/dpipe/control.sock",
			HTTP: &HTTPConfig{Listen: "0.0.0.0:80", Hosts: map[string]HTTPHost{
				"one.vm.local": {Host: "10.64.0.2", UnauthenticatedPorts: []int{8000}, DefaultPort: 8000},
			}},
			Console: &ConsoleConfig{},
		}
	}

	cfg := base()
	if err := cfg.Validate(); err == nil {
		t.Error("console without auth: must fail validation")
	}

	cfg = base()
	cfg.Auth = &AuthConfig{ControlURL: "http://c/l", CookieSecretFile: "/etc/dpipe/secret"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("console with auth: %v", err)
	}
	if cfg.Console.remoteUser() != "ubuntu" {
		t.Errorf("remoteUser = %q", cfg.Console.remoteUser())
	}

	cfg = base()
	cfg.Auth = &AuthConfig{ControlURL: "http://c/l", CookieSecretFile: "/etc/dpipe/secret"}
	cfg.HTTPS = &HTTPSConfig{Listen: "0.0.0.0:443"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("console together with the https ingress: %v", err)
	}
}
