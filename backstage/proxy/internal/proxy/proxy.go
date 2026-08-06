package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"proxy/internal/control"
	"proxy/internal/httpsniff"
	"proxy/internal/xnet"
)

const (
	defaultDialTimeout  = 5 * time.Second
	defaultSniffTimeout = 5 * time.Second
)

// Proxy is the control plane: it owns the public ingress listeners, decides where
// each connection goes and hands the connection to dpipe, so it holds no
// long-lived connection state and can be redeployed at any time.
type Proxy struct {
	cfg      *Config
	log      *slog.Logger
	router   *Router
	resolver *Resolver
	ctrl     *Client

	mu        sync.Mutex
	listeners []net.Listener

	stopOnce sync.Once
	stopped  chan struct{}
}

// New wires the router, resolver and control client.
func New(cfg *Config, log *slog.Logger) (*Proxy, error) {
	router := NewRouter(cfg)
	resolver, err := NewResolver(log, router, cfg)
	if err != nil {
		return nil, err
	}
	p := &Proxy{
		cfg:      cfg,
		log:      log,
		router:   router,
		resolver: resolver,
		stopped:  make(chan struct{}),
	}
	p.ctrl = NewClient(cfg.ControlSocket, resolver, log)
	return p, nil
}

// Client exposes the control client (tests, status tooling).
func (p *Proxy) Client() *Client { return p.ctrl }

// Run connects to dpipe, programs the configured listen_forwards, binds every
// ingress listener and serves until ctx is done.
func (p *Proxy) Run(ctx context.Context) error {
	if p.cfg.HTTPS != nil {
		p.log.Info("https ingress enabled: dpipe.tls.enabled must be true, certificates live in dpipe")
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

// bind binds every configured ingress listener and starts its accept loop.
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
	return nil
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

// Addrs returns the bound ingress addresses (tests).
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

// stop closes the ingress listeners. Connections already handed to dpipe live
// there and are unaffected.
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

// handleTCP: opaque TCP, routed by ingress listener.
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

// handleHTTP: plaintext HTTP, routed per connection by the first request's Host.
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

	target, ok := p.router.HostBackend(host)
	if !ok {
		log.Info("http deny: unknown host", "host", host)
		writeQuick(client, 502)
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
	// Replay the sniffed prefix (header block plus any body bytes read with it).
	if _, err := backend.Write(prefix); err != nil {
		log.Warn("http prefix replay failed", "target", target, "err", err)
		_ = client.Close()
		_ = backend.Close()
		return
	}
	p.handoffCopy(id, client, backend, control.ProtoHTTP)
}

// handleHTTPS hands the raw pre-TLS socket to dpipe, which terminates TLS and
// calls back with resolve{kind:"http"}. The proxy must not read or write it.
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

// handleSSH hands the raw pre-SSH socket to dpipe, which terminates SSH and
// calls back with resolve{kind:"ssh"}.
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

// writeQuick writes a minimal HTTP/1.1 error response.
func writeQuick(w io.Writer, code int) {
	reason := "Bad Request"
	switch code {
	case 400:
		reason = "Bad Request"
	case 502:
		reason = "Bad Gateway"
	case 503:
		reason = "Service Unavailable"
	}
	body := fmt.Sprintf("%d %s\n", code, reason)
	_, _ = fmt.Fprintf(w, "HTTP/1.1 %d %s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		code, reason, len(body), body)
}
