package dpipe

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dpipe/internal/control"
	"dpipe/internal/xnet"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func baseConfig(t *testing.T) *Config {
	t.Helper()
	dir := t.TempDir()
	return &Config{
		ControlSocket: filepath.Join(dir, "control.sock"),
		UpgradeSocket: filepath.Join(dir, "upgrade.sock"),
		LogLevel:      "error",
	}
}

func startServer(t *testing.T, cfg *Config) *Server {
	t.Helper()
	s, err := New(cfg, testLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Bind(); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	s.Start()
	t.Cleanup(s.stopAccepting)
	return s
}

// dialControl connects a fake proxy to the control socket. resolve, if non-nil,
// answers resolve requests.
func dialControl(t *testing.T, path string, resolve func(control.Msg) control.Msg) *control.Peer {
	t.Helper()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial control: %v", err)
	}
	uc := c.(*net.UnixConn)
	p := control.NewPeer(control.NewConn(uc), func(p *control.Peer, m control.Msg, fds []int) {
		control.CloseFDs(fds)
		if m.Type == control.TypeResolve && resolve != nil {
			_ = p.Send(resolve(m), nil)
			return
		}
		_ = p.Send(control.Err(m.ID, "unexpected request "+m.Type), nil)
	})
	go func() { _ = p.Serve() }()
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return ln.Addr().String()
}

func ctx5(t *testing.T) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// 1. copy: an echo backend, bytes flowing both ways.
func TestCopyEcho(t *testing.T) {
	cfg := baseConfig(t)
	s := startServer(t, cfg)
	p := dialControl(t, cfg.ControlSocket, nil)

	echo := echoServer(t)
	clientNear, clientFar := connPair(t)
	backend, err := net.Dial("tcp", echo)
	if err != nil {
		t.Fatalf("dial echo: %v", err)
	}

	ctx, cancel := ctx5(t)
	defer cancel()
	err = xnet.WithFD2(clientFar, backend, func(cfd, bfd int) error {
		rep, err := p.Request(ctx, control.Msg{
			V: control.Version, Type: control.TypeCopy, ID: "copy-1", Protocol: control.ProtoTCP,
		}, []int{cfd, bfd})
		if err != nil {
			return err
		}
		if rep.Type != control.TypeOK {
			return fmt.Errorf("reply %+v", rep)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("copy handoff: %v", err)
	}
	_ = clientFar.Close()
	_ = backend.Close()

	if _, err := clientNear.Write([]byte("hello dpipe")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, len("hello dpipe"))
	_ = clientNear.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(clientNear, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != "hello dpipe" {
		t.Fatalf("got %q", buf)
	}

	if got := s.Registry().Active(); got != 1 {
		t.Fatalf("active conns = %d, want 1", got)
	}

	sctx, scancel := ctx5(t)
	defer scancel()
	rep, err := p.Request(sctx, control.Msg{V: control.Version, Type: control.TypeStatus, ID: "st-1"}, nil)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if rep.ActiveConns != 1 || rep.Draining {
		t.Fatalf("status = %+v", rep)
	}
}

// 2. listen_forward + stop.
func TestListenForwardAndStop(t *testing.T) {
	cfg := baseConfig(t)
	startServer(t, cfg)
	p := dialControl(t, cfg.ControlSocket, nil)

	echo := echoServer(t)
	listen := freePort(t)

	ctx, cancel := ctx5(t)
	defer cancel()
	rep, err := p.Request(ctx, control.Msg{
		V: control.Version, Type: control.TypeListenForward, ID: "fwd-1",
		Listen: listen, Target: echo,
	}, nil)
	if err != nil {
		t.Fatalf("listen_forward: %v", err)
	}
	if rep.Type != control.TypeOK {
		t.Fatalf("reply %+v", rep)
	}

	c, err := net.Dial("tcp", listen)
	if err != nil {
		t.Fatalf("dial forward: %v", err)
	}
	if _, err := c.Write([]byte("fwd")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 3)
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != "fwd" {
		t.Fatalf("got %q", buf)
	}
	_ = c.Close()

	sctx, scancel := ctx5(t)
	defer scancel()
	if _, err := p.Request(sctx, control.Msg{V: control.Version, Type: control.TypeStop, ID: "fwd-1"}, nil); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if c, err := net.Dial("tcp", listen); err == nil {
		_ = c.Close()
		t.Fatal("forward listener still accepting after stop")
	}
}

// freePort returns a 127.0.0.1 address that was free a moment ago.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// 4. TLS termination: handshake, Host sniff, resolve, backend copy.
func TestTLSTermination(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "host=%s proto=%s body=%s", r.Host, r.Proto, body)
	}))
	t.Cleanup(backend.Close)
	backendAddr := backend.Listener.Addr().String()

	dir := t.TempDir()
	certPath, keyPath, pool := writeSelfSignedCert(t, dir, "vm1.local")

	cfg := baseConfig(t)
	cfg.TLS = TLSConfig{
		Enabled:    true,
		Certs:      []CertConfig{{SNI: "vm1.local", Cert: certPath, Key: keyPath}},
		MinVersion: "1.2",
	}
	startServer(t, cfg)

	p := dialControl(t, cfg.ControlSocket, func(m control.Msg) control.Msg {
		if m.Kind == control.KindHTTP && m.Host == "vm1.local" {
			return control.Msg{V: control.Version, Type: control.TypeResolved, ID: m.ID,
				Authorized: true, Target: backendAddr}
		}
		return control.Msg{V: control.Version, Type: control.TypeResolved, ID: m.ID}
	})

	t.Run("authorized host", func(t *testing.T) {
		resp := tlsRoundTrip(t, p, cfg, pool, "vm1.local", "POST /echo HTTP/1.1\r\nHost: vm1.local\r\nContent-Length: 4\r\nConnection: close\r\n\r\nping")
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		want := "host=vm1.local proto=HTTP/1.1 body=ping"
		if string(body) != want {
			t.Fatalf("body = %q, want %q", body, want)
		}
	})

	t.Run("unknown host is 502", func(t *testing.T) {
		resp := tlsRoundTrip(t, p, cfg, pool, "vm1.local", "GET / HTTP/1.1\r\nHost: nope.local\r\nConnection: close\r\n\r\n")
		if resp.StatusCode != 502 {
			t.Fatalf("status = %d, want 502", resp.StatusCode)
		}
	})
}

// 4b. The proxy owns the auth policy: dpipe forwards the request details it asks
// for and writes back whatever response it decides on.
func TestTLSAuthPolicyIsTheProxys(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "served")
	}))
	t.Cleanup(backend.Close)

	dir := t.TempDir()
	certPath, keyPath, pool := writeSelfSignedCert(t, dir, "vm1.local")

	cfg := baseConfig(t)
	cfg.TLS = TLSConfig{
		Enabled:    true,
		Certs:      []CertConfig{{SNI: "vm1.local", Cert: certPath, Key: keyPath}},
		MinVersion: "1.2",
	}
	startServer(t, cfg)

	var mu sync.Mutex
	var got control.Msg
	p := dialControl(t, cfg.ControlSocket, func(m control.Msg) control.Msg {
		mu.Lock()
		got = m
		mu.Unlock()
		reply := control.Msg{V: control.Version, Type: control.TypeResolved, ID: m.ID}
		switch {
		case strings.Contains(m.Cookie, "session=good"):
			reply.Authorized, reply.Target = true, backend.Listener.Addr().String()
		case strings.HasPrefix(m.Path, "/callback"):
			reply.Status, reply.Location, reply.SetCookie = 302, "/dash", "session=good; Path=/"
		case strings.Contains(m.Accept, "text/html"):
			reply.Status, reply.Location = 302, "https://control.local/login?rd=x"
		default:
			reply.Status = 401
		}
		return reply
	})

	lastResolve := func() control.Msg {
		mu.Lock()
		defer mu.Unlock()
		return got
	}

	t.Run("request details are forwarded", func(t *testing.T) {
		resp := tlsRoundTrip(t, p, cfg, pool, "vm1.local",
			"GET /dash?a=1 HTTP/1.1\r\nHost: vm1.local\r\nAccept: text/html\r\nCookie: session=stale\r\nConnection: close\r\n\r\n")
		if resp.StatusCode != 302 || resp.Header.Get("Location") != "https://control.local/login?rd=x" {
			t.Fatalf("status = %d, location = %q", resp.StatusCode, resp.Header.Get("Location"))
		}
		m := lastResolve()
		if m.Path != "/dash?a=1" || m.Cookie != "session=stale" || m.Accept != "text/html" {
			t.Fatalf("resolve = {path:%q cookie:%q accept:%q}", m.Path, m.Cookie, m.Accept)
		}
	})

	t.Run("non-browser request is refused", func(t *testing.T) {
		resp := tlsRoundTrip(t, p, cfg, pool, "vm1.local",
			"GET /api HTTP/1.1\r\nHost: vm1.local\r\nAccept: application/json\r\nConnection: close\r\n\r\n")
		if resp.StatusCode != 401 {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("login callback sets the cookie", func(t *testing.T) {
		resp := tlsRoundTrip(t, p, cfg, pool, "vm1.local",
			"GET /callback?token=t HTTP/1.1\r\nHost: vm1.local\r\nAccept: text/html\r\nConnection: close\r\n\r\n")
		if resp.StatusCode != 302 || resp.Header.Get("Location") != "/dash" {
			t.Fatalf("status = %d, location = %q", resp.StatusCode, resp.Header.Get("Location"))
		}
		if resp.Header.Get("Set-Cookie") != "session=good; Path=/" {
			t.Fatalf("set-cookie = %q", resp.Header.Get("Set-Cookie"))
		}
	})

	t.Run("authenticated request reaches the backend", func(t *testing.T) {
		resp := tlsRoundTrip(t, p, cfg, pool, "vm1.local",
			"GET / HTTP/1.1\r\nHost: vm1.local\r\nCookie: session=good\r\nConnection: close\r\n\r\n")
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "served" {
			t.Fatalf("body = %q", body)
		}
	})

	t.Run("oversize cookie is dropped, not truncated", func(t *testing.T) {
		big := "session=good; junk=" + strings.Repeat("x", control.MaxResolveCookie)
		resp := tlsRoundTrip(t, p, cfg, pool, "vm1.local",
			"GET / HTTP/1.1\r\nHost: vm1.local\r\nCookie: "+big+"\r\nConnection: close\r\n\r\n")
		if resp.StatusCode != 401 {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
		if c := lastResolve().Cookie; c != "" {
			t.Fatalf("cookie forwarded despite exceeding the budget: %q", c)
		}
	})
}

// 4c. A console resolve on the https ingress is served on the TLS connection
// dpipe already holds, never forwarded to a guest.
func TestTLSConsoleIsNotForwarded(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath, pool := writeSelfSignedCert(t, dir, "vm1.local")

	cfg := baseConfig(t)
	cfg.TLS = TLSConfig{
		Enabled:    true,
		Certs:      []CertConfig{{SNI: "vm1.local", Cert: certPath, Key: keyPath}},
		MinVersion: "1.2",
	}
	startServer(t, cfg)

	// The proxy authorized a console; this dpipe has consoles turned off, so the
	// only correct answer is a refusal — and never a connection to rep.Target.
	p := dialControl(t, cfg.ControlSocket, func(m control.Msg) control.Msg {
		return control.Msg{V: control.Version, Type: control.TypeResolved, ID: m.ID,
			Authorized: true, Protocol: control.ProtoConsole,
			Target: "10.64.0.2:22", RemoteUser: "ubuntu", Sub: "alice",
			WSKey: "dGhlIHNhbXBsZSBub25jZQ=="}
	})

	resp := tlsRoundTrip(t, p, cfg, pool, "vm1.local",
		"GET /?token=t HTTP/1.1\r\nHost: vm1.local\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
			"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n")
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404 (console not enabled)", resp.StatusCode)
	}
}

// tlsRoundTrip hands a raw socket to dpipe via tls_accept, then speaks TLS on
// the other end of that socket and returns the HTTP response.
func tlsRoundTrip(t *testing.T, p *control.Peer, cfg *Config, pool *x509.CertPool, sni, request string) *http.Response {
	t.Helper()
	clientNear, clientFar := connPair(t)

	ctx, cancel := ctx5(t)
	defer cancel()
	err := xnet.WithFD(clientFar, func(fd int) error {
		rep, err := p.Request(ctx, control.Msg{
			V: control.Version, Type: control.TypeTLSAccept, ID: control.NewID(), Protocol: control.ProtoTLS,
		}, []int{fd})
		if err != nil {
			return err
		}
		if rep.Type != control.TypeOK {
			return fmt.Errorf("reply %+v", rep)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("tls_accept handoff: %v", err)
	}
	_ = clientFar.Close()

	_ = clientNear.SetDeadline(time.Now().Add(10 * time.Second))
	tc := tls.Client(clientNear, &tls.Config{ServerName: sni, RootCAs: pool, MinVersion: tls.VersionTLS12})
	if err := tc.Handshake(); err != nil {
		t.Fatalf("client handshake: %v", err)
	}
	if got := tc.ConnectionState().NegotiatedProtocol; got != "http/1.1" && got != "" {
		t.Fatalf("ALPN = %q, want http/1.1 (no h2)", got)
	}
	if got := tc.ConnectionState().PeerCertificates[0].DNSNames; len(got) == 0 || got[0] != sni {
		t.Fatalf("served certificate DNS names = %v, want %s", got, sni)
	}
	if _, err := io.WriteString(tc, request); err != nil {
		t.Fatalf("write request: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(tc), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	t.Cleanup(func() { _ = tc.Close() })
	return resp
}

// writeSelfSignedCert writes a self-signed certificate usable both as the server
// certificate and as the client's trust root.
func writeSelfSignedCert(t *testing.T, dir, dnsName string) (certPath, keyPath string, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: dnsName},
		DNSNames:              []string{dnsName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certPath = filepath.Join(dir, dnsName+".crt")
	keyPath = filepath.Join(dir, dnsName+".key")

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	pool = x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("could not add certificate to pool")
	}
	return certPath, keyPath, pool
}
