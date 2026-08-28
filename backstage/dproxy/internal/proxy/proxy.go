package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"dproxy/internal/control"
	"dproxy/internal/httpsniff"
	"dproxy/internal/xnet"
)

const (
	defaultDialTimeout  = 5 * time.Second
	defaultSniffTimeout = 5 * time.Second
)

type Proxy struct {
	cfg      *Config
	log      *slog.Logger
	router   *Router
	resolver *Resolver
	auth     *Authenticator
	ctrl     *Client

	mu        sync.Mutex
	listeners []net.Listener

	stopOnce sync.Once
	stopped  chan struct{}
}

func New(cfg *Config, log *slog.Logger) (*Proxy, error) {
	router := NewRouter(cfg)
	auth, err := NewAuthenticator(cfg.Auth)
	if err != nil {
		return nil, err
	}
	resolver, err := NewResolver(log, router, auth, cfg)
	if err != nil {
		return nil, err
	}
	p := &Proxy{
		cfg:      cfg,
		log:      log,
		router:   router,
		resolver: resolver,
		auth:     auth,
		stopped:  make(chan struct{}),
	}
	p.ctrl = NewClient(cfg.ControlSocket, resolver, log)
	return p, nil
}

func (p *Proxy) Client() *Client { return p.ctrl }

func (p *Proxy) Run(ctx context.Context) error {
	if p.cfg.HTTPS != nil {
		p.log.Info("https ingress enabled: dpipe.tls.enabled must be true, certificates live in dpipe")
	}
	if p.cfg.HTTP != nil {
		for host, h := range p.cfg.HTTP.Hosts {
			p.log.Info("http host", "host", host, "target", h.Target(),
				"auth_required", h.NeedsAuth(h.DefaultPort))
		}
	}

	go p.ctrl.Run(ctx)
	if err := p.ctrl.WaitReady(ctx); err != nil {
		return fmt.Errorf("control connection: %w", err)
	}
	p.submitForwards(ctx)

	if err := p.bind(); err != nil {
		p.stop()
		return err
	}

	<-ctx.Done()
	p.log.Info("stopping: closing listeners, handed-off connections are unaffected")
	p.stop()
	p.ctrl.Close()
	return nil
}

func (p *Proxy) submitForwards(ctx context.Context) {
	for _, f := range p.cfg.ListenForwards {
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		id, err := p.ctrl.ListenForward(rctx, f.Listen, f.Target)
		cancel()
		if err != nil {
			p.log.Warn("listen_forward failed", "listen", f.Listen, "target", f.Target, "err", err)
			continue
		}
		p.log.Info("listen_forward programmed", "id", id, "listen", f.Listen, "target", f.Target)
	}
}

func (p *Proxy) bind() error {
	if c := p.cfg.HTTP; c != nil {
		ln, err := p.listen("http", c.Listen, c.Reuseport)
		if err != nil {
			return err
		}
		go p.acceptLoop("http", ln, func(conn net.Conn) { p.handleHTTP(conn) })
	}
	if c := p.cfg.HTTPS; c != nil {
		ln, err := p.listen("https", c.Listen, c.Reuseport)
		if err != nil {
			return err
		}
		go p.acceptLoop("https", ln, func(conn net.Conn) { p.handleHTTPS(conn) })
	}
	for _, route := range p.router.TCPRoutes() {
		ln, err := p.listen("tcp", route.Listen, route.Reuseport)
		if err != nil {
			return err
		}
		r := route
		go p.acceptLoop("tcp", ln, func(conn net.Conn) { p.handleTCP(r, conn) })
	}
	if c := p.cfg.SSH; c != nil {
		ln, err := p.listen("ssh", c.Listen, c.Reuseport)
		if err != nil {
			return err
		}
		go p.acceptLoop("ssh", ln, func(conn net.Conn) { p.handleSSH(conn) })
	}
	if c := p.cfg.Site; c != nil {
		page, err := os.ReadFile(c.HTMLFile)
		if err != nil {
			return fmt.Errorf("read site.html_file %s: %w", c.HTMLFile, err)
		}
		ln, err := p.listen("site", c.listen(), false)
		if err != nil {
			return err
		}
		p.log.Info("site hosts", "hosts", c.Hosts, "target", ln.Addr().String())
		go p.serveSite(ln, page)
	}
	return nil
}

func (p *Proxy) serveSite(ln net.Listener, page []byte) {
	srv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Length", strconv.Itoa(len(page)))
			_, _ = w.Write(page)
		}),
	}
	if err := srv.Serve(ln); err != nil && !errors.Is(err, net.ErrClosed) {
		p.log.Warn("site listener stopped", "err", err)
	}
}

func (p *Proxy) listen(kind, addr string, reuseport bool) (net.Listener, error) {
	ln, err := xnet.Listen("tcp", addr, reuseport)
	if err != nil {
		return nil, fmt.Errorf("listen %s on %s: %w", kind, addr, err)
	}
	p.mu.Lock()
	p.listeners = append(p.listeners, ln)
	p.mu.Unlock()
	p.log.Info("ingress listening", "kind", kind, "addr", ln.Addr().String(), "reuseport", reuseport)
	return ln, nil
}

func (p *Proxy) Addrs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.listeners))
	for _, ln := range p.listeners {
		out = append(out, ln.Addr().String())
	}
	return out
}

func (p *Proxy) acceptLoop(kind string, ln net.Listener, handle func(net.Conn)) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-p.stopped:
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			p.log.Warn("accept failed", "kind", kind, "err", err)
			return
		}
		go handle(conn)
	}
}

func (p *Proxy) stop() {
	p.stopOnce.Do(func() {
		close(p.stopped)
		p.mu.Lock()
		for _, ln := range p.listeners {
			_ = ln.Close()
		}
		p.mu.Unlock()
	})
}

func (p *Proxy) handleTCP(route TCPRoute, client net.Conn) {
	id := control.NewID()
	log := p.log.With("id", id, "protocol", control.ProtoTCP, "client", client.RemoteAddr().String())
	log.Info("tcp accept", "target", route.Target)

	backend, err := net.DialTimeout("tcp", route.Target, p.cfg.DialTimeout.Or(defaultDialTimeout))
	if err != nil {
		log.Warn("tcp backend dial failed", "target", route.Target, "err", err)
		_ = client.Close()
		return
	}
	p.handoffCopy(id, client, backend, control.ProtoTCP)
}

func (p *Proxy) authorizeHTTP(log *slog.Logger, client net.Conn, host string, prefix []byte) bool {
	entry, ok := p.router.HostEntry(host)
	if !ok || !entry.NeedsAuth(entry.DefaultPort) {
		return true
	}

	req, err := parseRequest(prefix)
	if err != nil {
		log.Warn("http auth: unparsable request", "host", host, "err", err)
		writeQuick(client, 400)
		return false
	}

	v := authorizeRequest(log, p.router, p.auth, host, req)
	if v.ok {
		return true
	}
	if v.status == http.StatusFound {
		writeRedirect(client, v.location, v.setCookie)
	} else {
		writeUnauthorized(client)
	}
	return false
}

func (p *Proxy) handleHTTP(client net.Conn) {
	id := control.NewID()
	log := p.log.With("id", id, "protocol", control.ProtoHTTP, "client", client.RemoteAddr().String())

	_ = client.SetReadDeadline(time.Now().Add(p.cfg.HTTPSniffTimeout.Or(defaultSniffTimeout)))
	prefix, host, err := httpsniff.ReadHeaderBlock(client, p.cfg.HTTPSniffMaxBytes)
	_ = client.SetReadDeadline(time.Time{})
	if err != nil {
		log.Warn("http sniff failed", "err", err)
		writeQuick(client, 400)
		_ = client.Close()
		return
	}

	if _, published := p.router.HostEntry(host); !published {
		if vmHost, ok := consoleVMHost(p.router, p.cfg.Console, host); ok {
			log.Info("console route", "host", host, "vm_host", vmHost)
			p.handleConsole(log, client, host, vmHost, prefix)
			return
		}
	}

	target, ok := p.router.HostBackend(host)
	if !ok {
		log.Info("http deny: unknown host", "host", host)
		writeQuick(client, 502)
		_ = client.Close()
		return
	}
	if !p.authorizeHTTP(log, client, host, prefix) {
		_ = client.Close()
		return
	}
	log.Info("http route", "host", host, "target", target)

	backend, err := net.DialTimeout("tcp", target, p.cfg.DialTimeout.Or(defaultDialTimeout))
	if err != nil {
		log.Warn("http backend dial failed", "target", target, "err", err)
		writeQuick(client, 502)
		_ = client.Close()
		return
	}
	if _, err := backend.Write(prefix); err != nil {
		log.Warn("http prefix replay failed", "target", target, "err", err)
		_ = client.Close()
		_ = backend.Close()
		return
	}
	p.handoffCopy(id, client, backend, control.ProtoHTTP)
}

func (p *Proxy) handleHTTPS(client net.Conn) {
	id := control.NewID()
	log := p.log.With("id", id, "protocol", control.ProtoTLS, "client", client.RemoteAddr().String())
	log.Info("https accept: handing raw socket to dpipe")

	err := p.handoffTLSAccept(id, client)
	_ = client.Close()
	if err != nil {
		log.Warn("tls_accept handoff failed", "err", err)
	}
}

func (p *Proxy) handleSSH(client net.Conn) {
	id := control.NewID()
	log := p.log.With("id", id, "protocol", control.ProtoSSH, "client", client.RemoteAddr().String())
	log.Info("ssh accept: handing raw socket to dpipe")

	err := p.handoffSSHAccept(id, client)
	_ = client.Close()
	if err != nil {
		log.Warn("ssh_accept handoff failed", "err", err)
	}
}

func writeQuick(w io.Writer, code int) {
	reason := "Bad Request"
	switch code {
	case 400:
		reason = "Bad Request"
	case 404:
		reason = "Not Found"
	case 502:
		reason = "Bad Gateway"
	case 503:
		reason = "Service Unavailable"
	}
	body := fmt.Sprintf("%d %s\n", code, reason)
	_, _ = fmt.Fprintf(w, "HTTP/1.1 %d %s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		code, reason, len(body), body)
}
