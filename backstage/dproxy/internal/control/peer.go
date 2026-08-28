package control

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var ErrClosed = errors.New("control: connection closed")

type Handler func(p *Peer, m Msg, fds []int)

type Peer struct {
	conn *Conn
	h    Handler

	mu      sync.Mutex
	waiters map[string]chan Msg
	closed  bool
	err     error

	done chan struct{}
}

func NewPeer(c *Conn, h Handler) *Peer {
	return &Peer{conn: c, h: h, waiters: make(map[string]chan Msg), done: make(chan struct{})}
}

func (p *Peer) Conn() *Conn { return p.conn }

func (p *Peer) Done() <-chan struct{} { return p.done }

func (p *Peer) Send(m Msg, fds []int) error {
	if m.V == 0 {
		m.V = Version
	}
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return ErrClosed
	}
	return p.conn.SendMsg(m, fds)
}

func (p *Peer) Request(ctx context.Context, m Msg, fds []int) (Msg, error) {
	if m.ID == "" {
		m.ID = NewID()
	}
	ch := make(chan Msg, 1)

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return Msg{}, ErrClosed
	}
	if _, dup := p.waiters[m.ID]; dup {
		p.mu.Unlock()
		return Msg{}, fmt.Errorf("control: duplicate request id %s", m.ID)
	}
	p.waiters[m.ID] = ch
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		delete(p.waiters, m.ID)
		p.mu.Unlock()
	}()

	if err := p.conn.SendMsg(m, fds); err != nil {
		return Msg{}, err
	}

	select {
	case rep := <-ch:
		if rep.Type == TypeError {
			return rep, fmt.Errorf("control: %s", rep.Error)
		}
		return rep, nil
	case <-ctx.Done():
		return Msg{}, ctx.Err()
	case <-p.done:
		return Msg{}, ErrClosed
	}
}

func (p *Peer) Serve() (err error) {
	defer p.shutdown()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("control: panic in read loop: %v", r)
			p.setErr(err)
		}
	}()
	for {
		m, fds, err := p.conn.RecvMsg()
		if err != nil {
			p.setErr(err)
			return err
		}
		if IsReply(m.Type) {
			CloseFDs(fds)
			p.deliver(m)
			continue
		}
		if p.h == nil {
			CloseFDs(fds)
			_ = p.Send(Err(m.ID, "unsupported request"), nil)
			continue
		}
		go p.dispatch(m, fds)
	}
}

func (p *Peer) dispatch(m Msg, fds []int) {
	defer func() {
		if r := recover(); r != nil {
			_ = p.Send(Err(m.ID, fmt.Sprintf("internal error handling %s", m.Type)), nil)
		}
	}()
	p.h(p, m, fds)
}

func (p *Peer) deliver(m Msg) {
	p.mu.Lock()
	ch := p.waiters[m.ID]
	delete(p.waiters, m.ID)
	p.mu.Unlock()
	if ch != nil {
		ch <- m
	}
}

func (p *Peer) setErr(err error) {
	p.mu.Lock()
	if p.err == nil {
		p.err = err
	}
	p.mu.Unlock()
}

func (p *Peer) Close() error {
	err := p.conn.Close()
	p.shutdown()
	return err
}

func (p *Peer) shutdown() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.waiters = make(map[string]chan Msg)
	p.mu.Unlock()
	_ = p.conn.Close()
	close(p.done)
}
