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

// sendBuffer is how many frames may queue for one agent before we consider it a
// slow consumer and drop the connection. Blocking the caller instead would let
// one wedged agent stall the whole control plane.
const sendBuffer = 32

var errAgentOffline = errors.New("agent is not connected")

// agentConn is one live agent socket. All writes go through the single writer
// goroutine started by run(), which is what makes Send safe to call from any
// number of goroutines.
type agentConn struct {
  agentID string
  ws      *websocket.Conn
  send    chan proto.Envelope

  closeOnce sync.Once
  closed    chan struct{}
}

func newAgentConn(agentID string, ws *websocket.Conn) *agentConn {
  return &agentConn{
    agentID: agentID,
    ws:      ws,
    send:    make(chan proto.Envelope, sendBuffer),
    closed:  make(chan struct{}),
  }
}

// close is idempotent: both the reader and the writer race to call it when the
// socket dies.
func (a *agentConn) close(code websocket.StatusCode, reason string) {
  a.closeOnce.Do(func() {
    close(a.closed)
    _ = a.ws.Close(code, reason)
  })
}

// writePump is the sole writer. It also drives the keepalive ping: a ping that
// does not come back within the timeout tears the connection down, which is how
// we notice a machine that vanished without closing its socket.
func (a *agentConn) writePump(ctx context.Context) {
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
        log.Printf("agent %s: could not marshal %s frame: %v", a.agentID, env.Type, err)
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

// Hub tracks the agents currently connected to *this* process. It is in-memory
// by design: running more than one control server means a connection is only
// reachable from the instance that owns it, so scaling out later needs a
// pub/sub fan-out (or routing by agent id), not a bigger map.
type Hub struct {
  mu    sync.RWMutex
  conns map[string]*agentConn
}

func NewHub() *Hub {
  return &Hub{conns: make(map[string]*agentConn)}
}

// add registers a connection, displacing any previous one for the same agent.
// Without this a flapping agent accumulates zombie sockets and its status
// column stops meaning anything.
func (h *Hub) add(c *agentConn) {
  h.mu.Lock()
  prev := h.conns[c.agentID]
  h.conns[c.agentID] = c
  h.mu.Unlock()

  if prev != nil {
    log.Printf("agent %s: replacing an existing connection", c.agentID)
    prev.close(websocket.StatusPolicyViolation, "replaced by a newer connection")
  }
}

// remove drops c only if it is still the current connection, so a stale
// goroutine cleaning up after being displaced cannot evict its replacement.
func (h *Hub) remove(c *agentConn) {
  h.mu.Lock()
  defer h.mu.Unlock()
  if cur, ok := h.conns[c.agentID]; ok && cur == c {
    delete(h.conns, c.agentID)
  }
}

// Connected reports whether the agent currently holds a socket to this process.
func (h *Hub) Connected(agentID string) bool {
  h.mu.RLock()
  defer h.mu.RUnlock()
  _, ok := h.conns[agentID]
  return ok
}

// enqueue hands a frame to the writer goroutine. A full buffer means the agent
// is not draining, so we drop it rather than block the caller.
func (a *agentConn) enqueue(env proto.Envelope) error {
  select {
  case a.send <- env:
    return nil
  case <-a.closed:
    return errAgentOffline
  default:
    a.close(websocket.StatusPolicyViolation, "send buffer full")
    return errors.New("agent send buffer full; connection dropped")
  }
}

// Send queues a frame for an agent. This is the push path: a job handler calls
// it and returns immediately.
func (h *Hub) Send(agentID string, env proto.Envelope) error {
  h.mu.RLock()
  c, ok := h.conns[agentID]
  h.mu.RUnlock()
  if !ok {
    return errAgentOffline
  }
  return c.enqueue(env)
}

// Broadcast queues a frame for every connected agent and reports how many it
// reached. For frames that carry no per-host fact: building one envelope and
// sending it to everyone is the difference between one settings read and one
// per host in the fleet.
//
// A send failure is dropped rather than returned. The frames that go this way
// are all "here is the current state of the world", so the agent that missed
// one gets the same thing on its next connect.
func (h *Hub) Broadcast(env proto.Envelope) int {
  h.mu.RLock()
  conns := make([]*agentConn, 0, len(h.conns))
  for _, c := range h.conns {
    conns = append(conns, c)
  }
  h.mu.RUnlock()

  sent := 0
  for _, c := range conns {
    if err := c.enqueue(env); err != nil {
      log.Printf("agent %s: could not deliver a broadcast: %v", c.agentID, err)
      continue
    }
    sent++
  }
  return sent
}

// ConnectedIDs lists the agents currently holding a connection.
//
// For frames that Broadcast cannot send, which is any file compiled from a
// particular host's VMs -- one envelope would carry one host's policy to all of
// them. The caller renders per id instead.
//
// A snapshot, not a live view: an agent can drop before the caller reaches it, and
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

// Kick closes an agent's connection, if any. Used when an agent is revoked or
// deleted so the socket does not outlive its authorization.
func (h *Hub) Kick(agentID, reason string) {
  h.mu.RLock()
  c, ok := h.conns[agentID]
  h.mu.RUnlock()
  if ok {
    c.close(websocket.StatusPolicyViolation, reason)
  }
}
