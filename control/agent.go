package main

import (
  "context"
  "encoding/json"
  "errors"
  "log"
  "net/http"
  "strings"
  "time"

  "github.com/coder/websocket"
  "github.com/google/uuid"
  "github.com/jackc/pgx/v5"
  "github.com/jackc/pgx/v5/pgxpool"
  "github.com/labstack/echo/v5"

  "control/internal/db"
  "control/internal/proto"
)

// Keepalive and I/O budgets for agent sockets.
const (
  pingInterval = 30 * time.Second
  pongTimeout  = 10 * time.Second
  writeTimeout = 10 * time.Second
  helloTimeout = 15 * time.Second
  // touchInterval throttles last_seen_at writes: a chatty agent should not turn
  // into a stream of UPDATEs.
  touchInterval = 30 * time.Second
)

// AgentHandler serves the agent-facing endpoints under /api/v1/agent. These are
// authenticated by enrollment key or agent token, never by the user JWT.
type AgentHandler struct {
  q    *db.Queries
  pool *pgxpool.Pool
  hub  *Hub
}

// --- enrollment ------------------------------------------------------------

// Enroll trades a valid enrollment key for a long-lived per-agent token. The
// key is consumed and the agent row written in one transaction, so a failed
// insert never burns a use of a use-limited key.
func (h *AgentHandler) Enroll(c *echo.Context) error {
  var req proto.EnrollRequest
  if err := c.Bind(&req); err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
  }
  req.Key = strings.TrimSpace(req.Key)
  req.MachineID = strings.TrimSpace(req.MachineID)
  if req.Key == "" || req.MachineID == "" {
    return echo.NewHTTPError(http.StatusBadRequest, "key and machine_id are required")
  }
  if h.pool == nil {
    return echo.NewHTTPError(http.StatusServiceUnavailable, "database unavailable")
  }

  ctx := c.Request().Context()
  tx, err := h.pool.Begin(ctx)
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not start transaction")
  }
  defer func() { _ = tx.Rollback(ctx) }()
  qtx := h.q.WithTx(tx)

  key, err := qtx.ConsumeEnrollmentKey(ctx, hashRefresh(req.Key))
  if err != nil {
    if errors.Is(err, pgx.ErrNoRows) {
      // Revoked, expired, exhausted and unknown are deliberately
      // indistinguishable so a caller cannot probe which keys exist.
      return echo.NewHTTPError(http.StatusUnauthorized, "invalid or expired enrollment key")
    }
    return echo.NewHTTPError(http.StatusInternalServerError, "could not validate enrollment key")
  }

  secret, err := newRefreshToken()
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not generate agent token")
  }

  agent, err := qtx.UpsertAgentByMachineID(ctx, db.UpsertAgentByMachineIDParams{
    MachineID:     req.MachineID,
    Hostname:      strings.TrimSpace(req.Hostname),
    TokenHash:     hashRefresh(secret),
    OS:            req.OS,
    OSVersion:     req.OSVersion,
    Arch:          req.Arch,
    AgentVersion:  req.Version,
    EnrolledKeyID: key.ID,
  })
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not register agent")
  }
  if err := tx.Commit(ctx); err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not complete enrollment")
  }

  agentID := uuid.UUID(agent.ID.Bytes).String()
  log.Printf("agent %s enrolled (machine_id=%s hostname=%s)", agentID, agent.MachineID, agent.Hostname)

  // The raw token leaves the server exactly once, here.
  return c.JSON(http.StatusCreated, proto.EnrollResponse{AgentID: agentID, Token: secret})
}

// --- websocket -------------------------------------------------------------

// Connect upgrades an authenticated agent to a persistent WebSocket and holds
// it open until either side goes away.
func (h *AgentHandler) Connect(c *echo.Context) error {
  raw := ""
  if hdr := c.Request().Header.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
    raw = strings.TrimPrefix(hdr, "Bearer ")
  }
  if raw == "" {
    return echo.NewHTTPError(http.StatusUnauthorized, "missing agent token")
  }

  // Authenticate before upgrading so a rejected agent sees a real HTTP status
  // rather than an opaque socket close.
  reqCtx := c.Request().Context()
  agent, err := h.q.GetAgentByTokenHash(reqCtx, hashRefresh(raw))
  if err != nil {
    if errors.Is(err, pgx.ErrNoRows) {
      return echo.NewHTTPError(http.StatusUnauthorized, "invalid agent token")
    }
    return echo.NewHTTPError(http.StatusInternalServerError, "could not authenticate agent")
  }
  if agent.Revoked {
    return echo.NewHTTPError(http.StatusUnauthorized, "agent has been revoked")
  }

  // InsecureSkipVerify disables the *Origin* check only. dagent is not a
  // browser and sends no Origin header; authentication is the bearer token
  // above, which this does not weaken.
  ws, err := websocket.Accept(c.Response(), c.Request(), &websocket.AcceptOptions{
    InsecureSkipVerify: true,
  })
  if err != nil {
    return nil // Accept already wrote a response
  }

  agentID := uuid.UUID(agent.ID.Bytes).String()
  h.serveAgent(agent, agentID, c.RealIP(), ws)
  return nil
}

// serveAgent owns the connection for its lifetime.
func (h *AgentHandler) serveAgent(agent db.Agent, agentID, remoteIP string, ws *websocket.Conn) {
  // Detached from the request context: that one is cancelled as soon as the
  // handler returns, which for a hijacked connection is immediately.
  ctx, cancel := context.WithCancel(context.Background())
  defer cancel()

  conn := newAgentConn(agentID, ws)
  h.hub.add(conn)

  if err := h.q.SetAgentOnline(ctx, db.SetAgentOnlineParams{ID: agent.ID, LastIP: remoteIP}); err != nil {
    log.Printf("agent %s: could not mark online: %v", agentID, err)
  }

  defer func() {
    h.hub.remove(conn)
    conn.close(websocket.StatusNormalClosure, "closing")
    // A fresh context: ctx is already cancelled by the time this runs.
    octx, ocancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer ocancel()
    if err := h.q.SetAgentOffline(octx, agent.ID); err != nil {
      log.Printf("agent %s: could not mark offline: %v", agentID, err)
    }
    log.Printf("agent %s disconnected", agentID)
  }()

  go conn.writePump(ctx)

  // Close the socket as soon as the writer gives up (failed ping or write),
  // so the blocking Read below returns instead of hanging.
  go func() {
    <-conn.closed
    cancel()
  }()

  if err := h.handshake(ctx, conn, agent, agentID); err != nil {
    log.Printf("agent %s: handshake failed: %v", agentID, err)
    conn.close(websocket.StatusPolicyViolation, "handshake failed")
    return
  }
  log.Printf("agent %s connected (hostname=%s ip=%s)", agentID, agent.Hostname, remoteIP)

  h.readLoop(ctx, conn, agent, agentID)
}

// handshake requires `hello` as the very first frame and answers `hello_ack`.
// The facts it carries refresh the agent row -- an agent that was upgraded or
// renamed since enrollment reports the truth here.
func (h *AgentHandler) handshake(ctx context.Context, conn *agentConn, agent db.Agent, agentID string) error {
  hctx, cancel := context.WithTimeout(ctx, helloTimeout)
  defer cancel()

  env, err := readEnvelope(hctx, conn.ws)
  if err != nil {
    return err
  }
  if env.Type != proto.TypeHello {
    return errors.New("expected hello, got " + string(env.Type))
  }

  var hello proto.Hello
  if err := json.Unmarshal(env.Payload, &hello); err != nil {
    return err
  }

  if err := h.q.UpdateAgentFacts(hctx, db.UpdateAgentFactsParams{
    ID:           agent.ID,
    Hostname:     hello.Hostname,
    OS:           hello.OS,
    OSVersion:    hello.OSVersion,
    Arch:         hello.Arch,
    AgentVersion: hello.Version,
  }); err != nil {
    log.Printf("agent %s: could not update facts: %v", agentID, err)
  }

  ack, err := proto.NewEnvelope(proto.TypeHelloAck, env.ID, proto.HelloAck{
    AgentID:    agentID,
    ServerTime: time.Now().UTC(),
  })
  if err != nil {
    return err
  }
  // Straight to this connection, not via the hub: if a replacement socket has
  // already displaced us, the ack must not go to it.
  return conn.enqueue(ack)
}

// readLoop drains frames until the connection dies. There are no job types
// yet, so anything other than a result is logged and ignored -- adding one
// later is a new case, not a restructure.
func (h *AgentHandler) readLoop(ctx context.Context, conn *agentConn, agent db.Agent, agentID string) {
  lastTouch := time.Now()

  for {
    env, err := readEnvelope(ctx, conn.ws)
    if err != nil {
      if ctx.Err() == nil && websocket.CloseStatus(err) == -1 {
        log.Printf("agent %s: read error: %v", agentID, err)
      }
      conn.close(websocket.StatusNormalClosure, "read loop ended")
      return
    }

    if time.Since(lastTouch) >= touchInterval {
      lastTouch = time.Now()
      if err := h.q.TouchAgentLastSeen(ctx, agent.ID); err != nil {
        log.Printf("agent %s: could not touch last_seen: %v", agentID, err)
      }
    }

    switch env.Type {
    case proto.TypeResult:
      // No job dispatch yet; there is nothing waiting on a correlation id.
      log.Printf("agent %s: result for job %s: %s", agentID, env.ID, env.Payload)
    case proto.TypeError:
      var p proto.ErrorPayload
      _ = json.Unmarshal(env.Payload, &p)
      log.Printf("agent %s reported an error: %s", agentID, p.Message)
    default:
      log.Printf("agent %s: ignoring unexpected frame type %q", agentID, env.Type)
    }
  }
}

// readEnvelope reads one text frame and decodes it.
func readEnvelope(ctx context.Context, ws *websocket.Conn) (proto.Envelope, error) {
  _, data, err := ws.Read(ctx)
  if err != nil {
    return proto.Envelope{}, err
  }
  var env proto.Envelope
  if err := json.Unmarshal(data, &env); err != nil {
    return proto.Envelope{}, err
  }
  return env, nil
}
