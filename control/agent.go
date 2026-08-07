package main

import (
  "context"
  "encoding/json"
  "errors"
  "log"
  "net/http"
  "os"
  "strings"
  "time"

  "github.com/coder/websocket"
  "github.com/google/uuid"
  "github.com/jackc/pgx/v5"
  "github.com/jackc/pgx/v5/pgtype"
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

  // openEnrollment lets an agent enrol with no key at all. Off unless
  // AGENT_OPEN_ENROLLMENT says otherwise; see openEnrollment().
  openEnrollment bool
}

// openEnrollment reads the one setting that decides whether a machine can join
// the fleet unauthenticated. Anything that reaches /enroll can then become an
// agent, so this belongs behind a network the operator controls.
func openEnrollment() bool {
  switch strings.ToLower(strings.TrimSpace(os.Getenv("AGENT_OPEN_ENROLLMENT"))) {
  case "1", "true", "yes", "on":
    return true
  }
  return false
}

// --- enrollment ------------------------------------------------------------

// Enroll trades a valid enrollment key for a long-lived per-agent token. The
// key is consumed and the agent row written in one transaction, so a failed
// insert never burns a use of a use-limited key.
//
// With open enrollment on, a request may carry no key. That path is deliberately
// narrower than the keyed one: it can register a machine_id the server has not
// seen, and nothing else. Re-enrolling an existing machine rewrites its token,
// and without a key to prove authorization that would be a takeover of any agent
// whose machine_id a caller could guess.
func (h *AgentHandler) Enroll(c *echo.Context) error {
  var req proto.EnrollRequest
  if err := c.Bind(&req); err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
  }
  req.Key = strings.TrimSpace(req.Key)
  req.MachineID = strings.TrimSpace(req.MachineID)
  if req.MachineID == "" {
    return echo.NewHTTPError(http.StatusBadRequest, "machine_id is required")
  }
  if req.Key == "" && !h.openEnrollment {
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

  // Null for a keyless enrollment: no key was consumed, so there is none to
  // point at, and the column already allows it.
  var enrolledKeyID pgtype.UUID
  if req.Key != "" {
    key, err := qtx.ConsumeEnrollmentKey(ctx, hashRefresh(req.Key))
    if err != nil {
      if errors.Is(err, pgx.ErrNoRows) {
        // Revoked, expired, exhausted and unknown are deliberately
        // indistinguishable so a caller cannot probe which keys exist.
        return echo.NewHTTPError(http.StatusUnauthorized, "invalid or expired enrollment key")
      }
      return echo.NewHTTPError(http.StatusInternalServerError, "could not validate enrollment key")
    }
    enrolledKeyID = key.ID
  } else {
    // In the same transaction as the upsert below, so two simultaneous keyless
    // requests for one machine_id cannot both pass this check.
    switch _, err := qtx.GetAgentByMachineID(ctx, req.MachineID); {
    case err == nil:
      return echo.NewHTTPError(http.StatusConflict,
        "this machine is already enrolled; keyless enrollment cannot re-issue its token, so use an enrollment key or delete the agent first")
    case !errors.Is(err, pgx.ErrNoRows):
      return echo.NewHTTPError(http.StatusInternalServerError, "could not check machine registration")
    }
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
    EnrolledKeyID: enrolledKeyID,
  })
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not register agent")
  }
  if err := tx.Commit(ctx); err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not complete enrollment")
  }

  agentID := uuid.UUID(agent.ID.Bytes).String()
  how := "key"
  if req.Key == "" {
    how = "keyless"
  }
  log.Printf("agent %s enrolled via %s (machine_id=%s hostname=%s)", agentID, how, agent.MachineID, agent.Hostname)

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
    // Anything still pending was waiting on a result frame from the socket that
    // just died. It is never going to arrive, so the row must not keep claiming
    // the create is in progress.
    if err := h.q.FailPendingVMsForAgent(octx, db.FailPendingVMsForAgentParams{
      AgentID:   agent.ID,
      LastError: "the agent disconnected before reporting the result",
    }); err != nil {
      log.Printf("agent %s: could not fail pending vms: %v", agentID, err)
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

// readLoop drains frames until the connection dies.
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
      h.handleResult(ctx, agent, agentID, env)
    case proto.TypeMetrics:
      h.handleMetrics(ctx, agent, agentID, env)
    case proto.TypeInventory:
      h.handleInventory(ctx, agent, agentID, env)
    case proto.TypeError:
      var p proto.ErrorPayload
      _ = json.Unmarshal(env.Payload, &p)
      log.Printf("agent %s reported an error: %s", agentID, p.Message)
    default:
      log.Printf("agent %s: ignoring unexpected frame type %q", agentID, env.Type)
    }
  }
}

// handleResult settles the row the job was created from. The envelope id *is*
// the vms row id, which is what makes the correlation a lookup rather than a
// table of in-flight jobs that a restart would lose.
func (h *AgentHandler) handleResult(ctx context.Context, agent db.Agent, agentID string, env proto.Envelope) {
  var res proto.JobResult
  if err := json.Unmarshal(env.Payload, &res); err != nil {
    log.Printf("agent %s: could not decode the result for job %s: %v", agentID, env.ID, err)
    return
  }
  rowID, err := parseUUID(env.ID)
  if err != nil {
    log.Printf("agent %s: result for job %s has an unusable correlation id", agentID, env.ID)
    return
  }

  switch res.Kind {
  case proto.KindVMCreate:
    h.settleCreate(ctx, agent, agentID, env.ID, rowID, res)
  case proto.KindVMStop:
    h.settleEnd(ctx, agentID, env.ID, rowID, res, "stopped")
  case proto.KindVMStart:
    h.settleEnd(ctx, agentID, env.ID, rowID, res, "running")
  case proto.KindVMDestroy:
    h.settleEnd(ctx, agentID, env.ID, rowID, res, "gone")
  default:
    log.Printf("agent %s: result for job %s of unknown kind %q", agentID, env.ID, res.Kind)
  }
}

// settleEnd records the outcome of a start, stop or destroy. On success the status is
// moved now rather than waiting for the next inventory tick, which is what makes
// the button feel like it did something. On failure only the message is stored:
// a stop that failed probably leaves the VM running, and overwriting the status
// would replace a true claim with a guess.
func (h *AgentHandler) settleEnd(ctx context.Context, agentID, jobID string, rowID pgtype.UUID, res proto.JobResult, status string) {
  if !res.OK {
    msg := res.Error
    if msg == "" {
      msg = "the agent reported a failure with no message"
    }
    if err := h.q.SetVMLastError(ctx, db.SetVMLastErrorParams{ID: rowID, LastError: msg}); err != nil {
      log.Printf("agent %s: could not record the failed %s of vm %s: %v", agentID, res.Kind, jobID, err)
    }
    log.Printf("agent %s: %s of vm %s failed: %s", agentID, res.Kind, jobID, msg)
    return
  }
  if err := h.q.SetVMStatus(ctx, db.SetVMStatusParams{ID: rowID, Status: status}); err != nil {
    log.Printf("agent %s: could not mark vm %s %s: %v", agentID, jobID, status, err)
    return
  }
  log.Printf("agent %s: vm %s is now %s", agentID, jobID, status)
}

func (h *AgentHandler) settleCreate(ctx context.Context, agent db.Agent, agentID, jobID string, rowID pgtype.UUID, res proto.JobResult) {
  if !res.OK || res.VM == nil {
    msg := res.Error
    if msg == "" {
      msg = "the agent reported a failure with no message"
    }
    if err := h.q.MarkVMFailed(ctx, db.MarkVMFailedParams{ID: rowID, LastError: msg}); err != nil {
      log.Printf("agent %s: could not record the failed vm %s: %v", agentID, jobID, err)
    }
    log.Printf("agent %s: vm %s failed: %s", agentID, jobID, msg)
    return
  }

  // An inventory report can beat the result frame here and adopt the very VM
  // this row is waiting for, which would collide on (agent_id, vm_id). Dropping
  // the adopted duplicate and claiming the id must happen together, or a failure
  // in between leaves two rows for one VM.
  tx, err := h.pool.Begin(ctx)
  if err != nil {
    log.Printf("agent %s: could not start a transaction for vm %s: %v", agentID, jobID, err)
    return
  }
  defer func() { _ = tx.Rollback(ctx) }()
  qtx := h.q.WithTx(tx)

  if err := qtx.DeleteAdoptedVM(ctx, db.DeleteAdoptedVMParams{
    AgentID: agent.ID,
    VMID:    res.VM.ID,
    ID:      rowID,
  }); err != nil {
    log.Printf("agent %s: could not clear the adopted duplicate of vm %s: %v", agentID, jobID, err)
    return
  }
  if err := qtx.MarkVMRunning(ctx, db.MarkVMRunningParams{
    ID:        rowID,
    VMID:      res.VM.ID,
    Name:      res.VM.Name,
    Boot:      res.VM.Boot,
    CPUs:      int32(res.VM.CPUs),
    MemoryMiB: int32(res.VM.MemoryMiB),
    IP:        res.VM.IP,
  }); err != nil {
    log.Printf("agent %s: could not record the running vm %s: %v", agentID, jobID, err)
    return
  }
  if err := tx.Commit(ctx); err != nil {
    log.Printf("agent %s: could not commit the running vm %s: %v", agentID, jobID, err)
    return
  }
  log.Printf("agent %s: vm %s running (local id %s, ip %s)", agentID, jobID, res.VM.ID, res.VM.IP)
}

// handleInventory reconciles the agent's report against the registry. This is
// what makes a VM created locally with `dagent vm create` appear in the control
// plane at all, and what notices one that has been removed on the host.
//
// The report is authoritative but not destructive: rows are upserted or marked
// 'gone', never deleted, so a VM that disappeared stays visible.
func (h *AgentHandler) handleInventory(ctx context.Context, agent db.Agent, agentID string, env proto.Envelope) {
  var inv proto.Inventory
  if err := json.Unmarshal(env.Payload, &inv); err != nil {
    log.Printf("agent %s: could not decode inventory: %v", agentID, err)
    return
  }

  seen := make([]string, 0, len(inv.VMs))
  for _, v := range inv.VMs {
    if v.ID == "" {
      continue // nothing to key on; the agent should never send this
    }
    seen = append(seen, v.ID)

    status := "stopped"
    started := pgtype.Timestamptz{}
    if v.Running {
      status = "running"
      // The host does not report when the boot happened, so its creation time is
      // the closest honest answer -- and only for a row we are learning about now.
      started = pgtype.Timestamptz{Time: v.CreatedAt, Valid: !v.CreatedAt.IsZero()}
    }
    created := pgtype.Timestamptz{Time: v.CreatedAt, Valid: !v.CreatedAt.IsZero()}
    if !created.Valid {
      created = pgtype.Timestamptz{Time: time.Now(), Valid: true}
    }

    if err := h.q.UpsertVMFromInventory(ctx, db.UpsertVMFromInventoryParams{
      AgentID:   agent.ID,
      VMID:      v.ID,
      Name:      v.Name,
      Status:    status,
      Boot:      v.Boot,
      CPUs:      int32(v.CPUs),
      MemoryMiB: int32(v.MemoryMiB),
      IP:        v.IP,
      CreatedAt: created,
      StartedAt: started,
    }); err != nil {
      log.Printf("agent %s: could not record vm %s from inventory: %v", agentID, v.ID, err)
    }
  }

  if err := h.q.MarkMissingVMsGone(ctx, db.MarkMissingVMsGoneParams{
    AgentID: agent.ID,
    VmIds:   seen,
  }); err != nil {
    log.Printf("agent %s: could not reconcile removed vms: %v", agentID, err)
  }
}

func (h *AgentHandler) handleMetrics(ctx context.Context, agent db.Agent, agentID string, env proto.Envelope) {
  var m proto.Metrics
  if err := json.Unmarshal(env.Payload, &m); err != nil {
    log.Printf("agent %s: could not decode metrics: %v", agentID, err)
    return
  }
  if err := h.q.UpdateAgentMetrics(ctx, db.UpdateAgentMetricsParams{
    ID:             agent.ID,
    CPUCount:       int32(m.CPUCount),
    CPUPercent:     m.CPUPercent,
    Load1:          m.Load1,
    Load5:          m.Load5,
    Load15:         m.Load15,
    MemTotalBytes:  m.MemTotalBytes,
    MemUsedBytes:   m.MemUsedBytes,
    DiskTotalBytes: m.DiskTotalBytes,
    DiskUsedBytes:  m.DiskUsedBytes,
    UptimeSeconds:  m.UptimeSeconds,
  }); err != nil {
    log.Printf("agent %s: could not store metrics: %v", agentID, err)
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
