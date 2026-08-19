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

// sendBuffer is how many frames may queue for one client before we consider it a
// slow consumer and drop the connection. Blocking the caller instead would let
// one wedged client stall the whole control plane.
const sendBuffer = 32

var errClientOffline = errors.New("client is not connected")

// clientConn is one live client socket. All writes go through the single writer
// goroutine started by run(), which is what makes Send safe to call from any
// number of goroutines.
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

// close is idempotent: both the reader and the writer race to call it when the
// socket dies.
func (a *clientConn) close(code websocket.StatusCode, reason string) {
	a.closeOnce.Do(func() {
		close(a.closed)
		_ = a.ws.Close(code, reason)
	})
}

// writePump is the sole writer. It also drives the keepalive ping: a ping that
// does not come back within the timeout tears the connection down, which is how
// we notice a machine that vanished without closing its socket.
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

// Hub tracks the clients currently connected to *this* process. It is in-memory
// by design: running more than one control server means a connection is only
// reachable from the instance that owns it, so scaling out later needs a
// pub/sub fan-out (or routing by client id), not a bigger map.
type Hub struct {
	mu    sync.RWMutex
	conns map[string]*clientConn
	// purge holds the row ids of VMs whose owner asked for the record to go with
	// the guest. A destroy job carries no such flag -- an operator's destroy and a
	// user's delete are the same frame -- so the intent waits here for the result
	// to come back.
	//
	// In memory for the same reason the connections are: it is only meaningful to
	// the process holding the socket the result will arrive on. Losing it to a
	// restart settles the row as 'gone' instead, which a second delete clears
	// outright since there is nothing left on the host by then.
	purge map[string]struct{}
}

func NewHub() *Hub {
	return &Hub{
		conns: make(map[string]*clientConn),
		purge: make(map[string]struct{}),
	}
}

// add registers a connection, displacing any previous one for the same client.
// Without this a flapping client accumulates zombie sockets and its status
// column stops meaning anything.
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

// remove drops c only if it is still the current connection, so a stale
// goroutine cleaning up after being displaced cannot evict its replacement.
func (h *Hub) remove(c *clientConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cur, ok := h.conns[c.clientID]; ok && cur == c {
		delete(h.conns, c.clientID)
	}
}

// Connected reports whether the client currently holds a socket to this process.
func (h *Hub) Connected(clientID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.conns[clientID]
	return ok
}

// enqueue hands a frame to the writer goroutine. A full buffer means the client
// is not draining, so we drop it rather than block the caller.
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

// Send queues a frame for an client. This is the push path: a job handler calls
// it and returns immediately.
func (h *Hub) Send(clientID string, env proto.Envelope) error {
	h.mu.RLock()
	c, ok := h.conns[clientID]
	h.mu.RUnlock()
	if !ok {
		return errClientOffline
	}
	return c.enqueue(env)
}

// Broadcast queues a frame for every connected client and reports how many it
// reached. For frames that carry no per-host fact: building one envelope and
// sending it to everyone is the difference between one settings read and one
// per host in the fleet.
//
// A send failure is dropped rather than returned. The frames that go this way
// are all "here is the current state of the world", so the client that missed
// one gets the same thing on its next connect.
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

// ConnectedIDs lists the clients currently holding a connection.
//
// For frames that Broadcast cannot send, which is any file compiled from a
// particular host's VMs -- one envelope would carry one host's policy to all of
// them. The caller renders per id instead.
//
// A snapshot, not a live view: an client can drop before the caller reaches it, and
// Send fails for that one rather than for the batch. That is the same tolerance
// Broadcast has, and for the same reason -- everything sent this way is resent on
// connect.
func (h *Hub) ConnectedIDs() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ids := make([]string, 0, len(h.conns))
	for id := range h.conns {
		ids = append(ids, id)
	}
	return ids
}

// MarkPurge records that this VM's row is to be deleted once its destroy comes
// back, not moved to 'gone'. Called before the job is sent, so the result cannot
// arrive before the mark is readable.
func (h *Hub) MarkPurge(rowID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.purge[rowID] = struct{}{}
}

// TakePurge reports whether a delete was asked for and forgets the mark. Taken
// rather than read so a later destroy of the same row -- an adopted VM reusing
// the id is not possible, but a retried job is -- does not inherit the decision.
func (h *Hub) TakePurge(rowID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.purge[rowID]
	delete(h.purge, rowID)
	return ok
}

// Kick closes an client's connection, if any. Used when an client is revoked or
// deleted so the socket does not outlive its authorization.
func (h *Hub) Kick(clientID, reason string) {
	h.mu.RLock()
	c, ok := h.conns[clientID]
	h.mu.RUnlock()
	if ok {
		c.close(websocket.StatusPolicyViolation, reason)
	}
}
