package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/coder/websocket"

	"control/internal/proto"
)

const sendBuffer = 32

var errClientOffline = errors.New("client is not connected")

type clientConn struct {
	clientID string
	ws       *websocket.Conn
	send     chan proto.Envelope

	closeOnce sync.Once
	closed    chan struct{}
}

func newClientConn(clientID string, ws *websocket.Conn) *clientConn {
	return &clientConn{
		clientID: clientID,
		ws:       ws,
		send:     make(chan proto.Envelope, sendBuffer),
		closed:   make(chan struct{}),
	}
}

func (a *clientConn) close(code websocket.StatusCode, reason string) {
	a.closeOnce.Do(func() {
		close(a.closed)
		_ = a.ws.Close(code, reason)
	})
}

func (a *clientConn) writePump(ctx context.Context) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			a.close(websocket.StatusGoingAway, "server shutting down")
			return
		case <-a.closed:
			return
		case env := <-a.send:
			b, err := json.Marshal(env)
			if err != nil {
				log.Printf("client %s: could not marshal %s frame: %v", a.clientID, env.Type, err)
				continue
			}
			wctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err = a.ws.Write(wctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				a.close(websocket.StatusInternalError, "write failed")
				return
			}
		case <-ticker.C:
			pctx, cancel := context.WithTimeout(ctx, pongTimeout)
			err := a.ws.Ping(pctx)
			cancel()
			if err != nil {
				a.close(websocket.StatusPolicyViolation, "ping timeout")
				return
			}
		}
	}
}

type Hub struct {
	mu    sync.RWMutex
	conns map[string]*clientConn
	purge map[string]struct{}
}

func NewHub() *Hub {
	return &Hub{
		conns: make(map[string]*clientConn),
		purge: make(map[string]struct{}),
	}
}

func (h *Hub) add(c *clientConn) {
	h.mu.Lock()
	prev := h.conns[c.clientID]
	h.conns[c.clientID] = c
	h.mu.Unlock()

	if prev != nil {
		log.Printf("client %s: replacing an existing connection", c.clientID)
		prev.close(websocket.StatusPolicyViolation, "replaced by a newer connection")
	}
}

func (h *Hub) remove(c *clientConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cur, ok := h.conns[c.clientID]; ok && cur == c {
		delete(h.conns, c.clientID)
	}
}

func (h *Hub) Connected(clientID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.conns[clientID]
	return ok
}

func (a *clientConn) enqueue(env proto.Envelope) error {
	select {
	case a.send <- env:
		return nil
	case <-a.closed:
		return errClientOffline
	default:
		a.close(websocket.StatusPolicyViolation, "send buffer full")
		return errors.New("client send buffer full; connection dropped")
	}
}

func (h *Hub) Send(clientID string, env proto.Envelope) error {
	h.mu.RLock()
	c, ok := h.conns[clientID]
	h.mu.RUnlock()
	if !ok {
		return errClientOffline
	}
	return c.enqueue(env)
}

func (h *Hub) Broadcast(env proto.Envelope) int {
	h.mu.RLock()
	conns := make([]*clientConn, 0, len(h.conns))
	for _, c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.RUnlock()

	sent := 0
	for _, c := range conns {
		if err := c.enqueue(env); err != nil {
			log.Printf("client %s: could not deliver a broadcast: %v", c.clientID, err)
			continue
		}
		sent++
	}
	return sent
}

func (h *Hub) ConnectedIDs() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ids := make([]string, 0, len(h.conns))
	for id := range h.conns {
		ids = append(ids, id)
	}
	return ids
}

func (h *Hub) MarkPurge(rowID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.purge[rowID] = struct{}{}
}

func (h *Hub) TakePurge(rowID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.purge[rowID]
	delete(h.purge, rowID)
	return ok
}

func (h *Hub) Kick(clientID, reason string) {
	h.mu.RLock()
	c, ok := h.conns[clientID]
	h.mu.RUnlock()
	if ok {
		c.close(websocket.StatusPolicyViolation, reason)
	}
}
