package proxy

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"dproxy/internal/control"
	"dproxy/internal/xnet"
)

type fakeDpipe struct {
	t    *testing.T
	path string
	ln   *net.UnixListener

	raw   chan net.Conn
	peers chan *control.Peer
}

func (f *fakeDpipe) Peer(t *testing.T) *control.Peer {
	t.Helper()
	select {
	case p := <-f.peers:
		return p
	case <-time.After(5 * time.Second):
		t.Fatal("proxy never opened a control connection")
		return nil
	}
}

func hostEntry(t *testing.T, addr string) HTTPHost {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("port %q: %v", port, err)
	}
	return HTTPHost{Host: host, UnauthenticatedPorts: []int{p}, DefaultPort: p}
}

func startFakeDpipe(t *testing.T) *fakeDpipe {
	t.Helper()
	path := filepath.Join(t.TempDir(), "control.sock")
	ln, err := xnet.ListenUnix(path)
	if err != nil {
		t.Fatalf("listen control socket: %v", err)
	}
	f := &fakeDpipe{
		t: t, path: path, ln: ln,
		raw:   make(chan net.Conn, 4),
		peers: make(chan *control.Peer, 4),
	}
	t.Cleanup(func() {
		_ = ln.Close()
		_ = os.Remove(path)
	})
	go f.accept()
	return f
}

func (f *fakeDpipe) accept() {
	for {
		c, err := f.ln.AcceptUnix()
		if err != nil {
			return
		}
		p := control.NewPeer(control.NewConn(c), f.handle)
		go func() { _ = p.Serve() }()
		select {
		case f.peers <- p:
		default:
		}
	}
}

func (f *fakeDpipe) handle(p *control.Peer, m control.Msg, fds []int) {
	switch m.Type {
	case control.TypeCopy:
		if len(fds) != 2 {
			control.CloseFDs(fds)
			_ = p.Send(control.Err(m.ID, "want 2 fds"), nil)
			return
		}
		client, err1 := xnet.FileConn(fds[0])
		backend, err2 := xnet.FileConn(fds[1])
		if err1 != nil || err2 != nil {
			_ = p.Send(control.Err(m.ID, "adopt failed"), nil)
			return
		}
		_ = p.Send(control.OK(m.ID), nil)
		go pipeConns(client, backend)

	case control.TypeSSHAccept, control.TypeTLSAccept:
		if len(fds) != 1 {
			control.CloseFDs(fds)
			_ = p.Send(control.Err(m.ID, "want 1 fd"), nil)
			return
		}
		c, err := xnet.FileConn(fds[0])
		if err != nil {
			_ = p.Send(control.Err(m.ID, err.Error()), nil)
			return
		}
		_ = p.Send(control.OK(m.ID), nil)
		select {
		case f.raw <- c:
		default:
			_ = c.Close()
		}

	case control.TypeListenForward:
		_ = p.Send(control.OK(m.ID), nil)

	default:
		control.CloseFDs(fds)
		_ = p.Send(control.Err(m.ID, "unexpected "+m.Type), nil)
	}
}

func pipeConns(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	copyHalf := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}
	go copyHalf(a, b)
	go copyHalf(b, a)
	wg.Wait()
	_ = a.Close()
	_ = b.Close()
}

func startProxy(t *testing.T, cfg *Config, wantListeners int) *Proxy {
	t.Helper()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}
	p, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = p.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(p.Addrs()) == wantListeners {
			return p
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("proxy did not bind %d listeners (got %v)", wantListeners, p.Addrs())
	return nil
}

func httpClientVia(addr string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
			},
			DisableKeepAlives: true,
		},
		Timeout: 5 * time.Second,
	}
}

func TestHTTPHostRouting(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, _ = io.WriteString(w, r.Method+" host="+r.Host+" body="+string(body))
	}))
	t.Cleanup(backend.Close)

	f := startFakeDpipe(t)
	cfg := &Config{
		ControlSocket: f.path,
		HTTP: &HTTPConfig{Listen: "127.0.0.1:0", Hosts: map[string]HTTPHost{
			"one.vm.local": hostEntry(t, backend.Listener.Addr().String()),
		}},
	}
	p := startProxy(t, cfg, 1)
	addr := p.Addrs()[0]
	client := httpClientVia(addr)

	resp, err := client.Get("http://one.vm.local/hello")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if got, want := string(body), "GET host=one.vm.local body="; got != want {
		t.Fatalf("GET body = %q, want %q", got, want)
	}

	presp, err := client.Post("http://one.vm.local/submit", "text/plain", strings.NewReader("payload-bytes"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	pbody, _ := io.ReadAll(presp.Body)
	presp.Body.Close()
	if got, want := string(pbody), "POST host=one.vm.local body=payload-bytes"; got != want {
		t.Fatalf("POST body = %q, want %q", got, want)
	}
}

func TestHTTPUnknownHostIs502(t *testing.T) {
	f := startFakeDpipe(t)
	cfg := &Config{
		ControlSocket: f.path,
		HTTP:          &HTTPConfig{Listen: "127.0.0.1:0", Hosts: map[string]HTTPHost{"one.vm.local": {Host: "127.0.0.1", DefaultPort: 1}}},
	}
	p := startProxy(t, cfg, 1)

	resp, err := httpClientVia(p.Addrs()[0]).Get("http://nope.vm.local/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 502 {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
}

func TestTCPEcho(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = echo.Close() })
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()

	f := startFakeDpipe(t)
	cfg := &Config{
		ControlSocket: f.path,
		TCP:           []TCPRoute{{Listen: "127.0.0.1:0", Target: echo.Addr().String(), Protocol: "tcp"}},
	}
	p := startProxy(t, cfg, 1)

	c, err := net.Dial("tcp", p.Addrs()[0])
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("opaque")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 6)
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != "opaque" {
		t.Fatalf("got %q", buf)
	}
}

func TestRawHandoffPreservesFirstBytes(t *testing.T) {
	f := startFakeDpipe(t)
	cfg := &Config{
		ControlSocket: f.path,
		HTTP:          &HTTPConfig{Listen: "127.0.0.1:0", Hosts: map[string]HTTPHost{"one.vm.local": {Host: "127.0.0.1", DefaultPort: 1}}},
		HTTPS:         &HTTPSConfig{Listen: "127.0.0.1:0"},
		SSH: &SSHConfig{Listen: "127.0.0.1:0", Users: []SSHUser{{
			PubKey:     authorizedLine(newTestKey(t), " test@example"),
			VMName:     "build",
			Target:     "127.0.0.1:22",
			RemoteUser: "dev",
		}}},
	}
	p := startProxy(t, cfg, 3)
	addrs := p.Addrs()

	for _, tc := range []struct {
		name  string
		addr  string
		first string
	}{
		{"https", addrs[1], "\x16\x03\x01client-hello"},
		{"ssh", addrs[2], "SSH-2.0-testclient\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := net.Dial("tcp", tc.addr)
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer c.Close()
			if _, err := io.WriteString(c, tc.first); err != nil {
				t.Fatalf("write: %v", err)
			}

			var raw net.Conn
			select {
			case raw = <-f.raw:
			case <-time.After(5 * time.Second):
				t.Fatal("dpipe never received the raw socket")
			}
			defer raw.Close()

			buf := make([]byte, len(tc.first))
			_ = raw.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := io.ReadFull(raw, buf); err != nil {
				t.Fatalf("read on handed-off socket: %v", err)
			}
			if string(buf) != tc.first {
				t.Fatalf("got %q, want %q", buf, tc.first)
			}

			if _, err := io.WriteString(raw, "reply"); err != nil {
				t.Fatalf("write back: %v", err)
			}
			rbuf := make([]byte, 5)
			_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := io.ReadFull(c, rbuf); err != nil {
				t.Fatalf("client read: %v", err)
			}
			if string(rbuf) != "reply" {
				t.Fatalf("client got %q", rbuf)
			}
		})
	}
}

func TestResolveOverControlConnection(t *testing.T) {
	f := startFakeDpipe(t)
	cfg := &Config{
		ControlSocket: f.path,
		HTTP:          &HTTPConfig{Listen: "127.0.0.1:0", Hosts: map[string]HTTPHost{"one.vm.local": {Host: "127.0.0.1", DefaultPort: 8001}}},
		HTTPS:         &HTTPSConfig{Listen: "127.0.0.1:0"},
	}
	startProxy(t, cfg, 2)
	peer := f.Peer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	hit, err := peer.Request(ctx, control.Msg{
		V: control.Version, Type: control.TypeResolve, ID: control.NewID(),
		Kind: control.KindHTTP, Host: "one.vm.local", SNI: "one.vm.local", ClientIP: "127.0.0.1",
	}, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !hit.Authorized || hit.Target != "127.0.0.1:8001" {
		t.Fatalf("resolve reply = %+v", hit)
	}

	miss, err := peer.Request(ctx, control.Msg{
		V: control.Version, Type: control.TypeResolve, ID: control.NewID(),
		Kind: control.KindHTTP, Host: "nope.vm.local",
	}, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if miss.Authorized {
		t.Fatalf("unknown host must be unauthorized: %+v", miss)
	}
}
