package proxy

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	"dproxy/internal/control"
)

const (
	reconnectMin = 100 * time.Millisecond
	reconnectMax = 5 * time.Second
)

type Client struct {
	path     string
	log      *slog.Logger
	resolver *Resolver

	mu   sync.RWMutex
	peer *control.Peer

	readyOnce sync.Once
	ready     chan struct{}

	closeOnce sync.Once
	closed    chan struct{}
}

func NewClient(path string, resolver *Resolver, log *slog.Logger) *Client {
	return &Client{
		path:     path,
		log:      log,
		resolver: resolver,
		ready:    make(chan struct{}),
		closed:   make(chan struct{}),
	}
}

func (c *Client) Run(ctx context.Context) {
	backoff := reconnectMin
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.closed:
			return
		default:
		}

		conn, err := net.Dial("unix", c.path)
		if err != nil {
			c.log.Warn("control connect failed", "socket", c.path, "err", err, "retry_in", backoff)
			if !c.sleep(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}
		uc, ok := conn.(*net.UnixConn)
		if !ok {
			_ = conn.Close()
			c.log.Error("control socket is not a unix connection", "socket", c.path)
			return
		}

		p := control.NewPeer(control.NewConn(uc), c.handleInbound)
		c.setPeer(p)
		c.readyOnce.Do(func() { close(c.ready) })
		backoff = reconnectMin
		c.log.Info("control connected", "socket", c.path)

		err = p.Serve()
		c.setPeer(nil)
		c.log.Warn("control connection lost", "err", err)

		if !c.sleep(ctx, reconnectMin) {
			return
		}
	}
}

func nextBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > reconnectMax {
		return reconnectMax
	}
	return d
}

func (c *Client) sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	case <-c.closed:
		return false
	}
}

func (c *Client) Close() {
	c.closeOnce.Do(func() { close(c.closed) })
	if p := c.current(); p != nil {
		_ = p.Close()
	}
}

func (c *Client) WaitReady(ctx context.Context) error {
	select {
	case <-c.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closed:
		return control.ErrClosed
	}
}

func (c *Client) setPeer(p *control.Peer) {
	c.mu.Lock()
	c.peer = p
	c.mu.Unlock()
}

func (c *Client) current() *control.Peer {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.peer
}

func (c *Client) peerOrErr() (*control.Peer, error) {
	p := c.current()
	if p == nil {
		return nil, errors.New("control connection is down")
	}
	return p, nil
}

func (c *Client) handleInbound(p *control.Peer, m control.Msg, fds []int) {
	control.CloseFDs(fds)
	if m.Type != control.TypeResolve {
		_ = p.Send(control.Err(m.ID, "unsupported request "+m.Type), nil)
		return
	}
	if err := p.Send(c.resolver.Handle(m), nil); err != nil {
		c.log.Warn("resolve reply failed", "id", m.ID, "err", err)
	}
}

func (c *Client) Copy(_ context.Context, id, protocol string, clientFD, backendFD int) error {
	p, err := c.peerOrErr()
	if err != nil {
		return err
	}
	return p.Send(control.Msg{
		V: control.Version, Type: control.TypeCopy, ID: id, Protocol: protocol,
	}, []int{clientFD, backendFD})
}

func (c *Client) SSHAccept(_ context.Context, id string, clientFD int) error {
	p, err := c.peerOrErr()
	if err != nil {
		return err
	}
	return p.Send(control.Msg{
		V: control.Version, Type: control.TypeSSHAccept, ID: id, Protocol: control.ProtoSSH,
	}, []int{clientFD})
}

func (c *Client) TLSAccept(_ context.Context, id string, clientFD int) error {
	p, err := c.peerOrErr()
	if err != nil {
		return err
	}
	return p.Send(control.Msg{
		V: control.Version, Type: control.TypeTLSAccept, ID: id, Protocol: control.ProtoTLS,
	}, []int{clientFD})
}

func (c *Client) ConsoleAccept(_ context.Context, m control.Msg, clientFD int) error {
	p, err := c.peerOrErr()
	if err != nil {
		return err
	}
	return p.Send(m, []int{clientFD})
}

func (c *Client) ListenForward(ctx context.Context, listen, target string) (string, error) {
	p, err := c.peerOrErr()
	if err != nil {
		return "", err
	}
	id := control.NewID()
	rep, err := p.Request(ctx, control.Msg{
		V: control.Version, Type: control.TypeListenForward, ID: id,
		Listen: listen, Target: target,
	}, nil)
	if err != nil {
		return "", err
	}
	if rep.ID != "" {
		return rep.ID, nil
	}
	return id, nil
}

func (c *Client) Stop(ctx context.Context, id string) error {
	p, err := c.peerOrErr()
	if err != nil {
		return err
	}
	_, err = p.Request(ctx, control.Msg{V: control.Version, Type: control.TypeStop, ID: id}, nil)
	return err
}

func (c *Client) Status(ctx context.Context) (conns, forwards int, draining bool, err error) {
	p, perr := c.peerOrErr()
	if perr != nil {
		return 0, 0, false, perr
	}
	rep, err := p.Request(ctx, control.Msg{
		V: control.Version, Type: control.TypeStatus, ID: control.NewID(),
	}, nil)
	if err != nil {
		return 0, 0, false, err
	}
	return rep.ActiveConns, rep.ListenForwards, rep.Draining, nil
}
