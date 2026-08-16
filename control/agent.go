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
  // proxy is carried only to be handed to pushProxyConfig: the generated
  // proxy.yaml names this server and carries the key its hosts verify with.
  proxy proxyAuthConfig
}

// openEnrollment reports whether a machine may join the fleet unauthenticated.
// Anything that reaches /enroll can then become an agent, so this belongs
// behind a network the operator controls.
//
// Read per request from the settings table rather than latched at boot: an
// admin turning it off in the UI has to take effect on the next enrollment
// attempt, not on the next restart. Fails closed if the read fails.
func (h *AgentHandler) openEnrollment(ctx context.Context) bool {
  return boolSetting(ctx, h.q, settingAgentOpenEnrollment)
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
  if h.pool == nil {
    return echo.NewHTTPError(http.StatusServiceUnavailable, "database unavailable")
  }

  ctx := c.Request().Context()

  // After the pool check: whether a keyless enrollment is allowed is itself a
  // database read now.
  if req.Key == "" && !h.openEnrollment(ctx) {
    return echo.NewHTTPError(http.StatusBadRequest, "key and machine_id are required")
  }
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

  // With exactly one domain configured there is no choice to make, so the agent
  // gets it. With none or several, it enrolls without one and an operator
  // decides. pgx.ErrNoRows covers both of those cases -- see GetSoleDomain.
  var domainID pgtype.UUID
  switch domain, err := qtx.GetSoleDomain(ctx); {
  case err == nil:
    domainID = domain.ID
  case !errors.Is(err, pgx.ErrNoRows):
    return echo.NewHTTPError(http.StatusInternalServerError, "could not resolve the agent domain")
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
    DomainID:      domainID,
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

  hello, err := h.handshake(ctx, conn, agent, agentID)
  if err != nil {
    log.Printf("agent %s: handshake failed: %v", agentID, err)
    conn.close(websocket.StatusPolicyViolation, "handshake failed")
    return
  }
  log.Printf("agent %s connected (hostname=%s ip=%s)", agentID, agent.Hostname, remoteIP)

  // The host may have been offline while destinations were added or removed, so
  // its local.rules is only trustworthy once this server has written it. Sent on
  // every connect rather than only when something changed: dagent's copy is not
  // knowable from here, and rewriting an identical file costs a reload.
  pushSuricataRules(ctx, h.q, h.hub, agent.ID)
  // Same reasoning for proxy.yaml: VMs may have come or gone while the host was
  // away. Cheap to send unconditionally -- the agent compares the file it is
  // given against the one on disk and only restarts proxy when they differ.
  pushProxyConfig(ctx, h.q, h.hub, h.proxy, agent.ID)
  // And vector.yaml: the settings it is built from may have changed while the
  // host was away, and this is also what installs vector on a host that has
  // just enrolled.
  pushVectorConfig(ctx, h.q, h.hub, agent.ID)
  // suricata.yaml and dpipe.yaml are sent here and nowhere else: neither is
  // built from anything that changes while a host is connected, so a connect is
  // the only moment either can be out of date. The suricata one needs the pool
  // the hello just carried, which is why it is not sent from anywhere that has
  // only an agent id.
  pushSuricataConfig(ctx, h.hub, agent.ID, hello.Pool)
  pushDpipeConfig(ctx, h.hub, agent.ID)

  h.readLoop(ctx, conn, agent, agentID)
}

// handshake requires `hello` as the very first frame and answers `hello_ack`.
// The facts it carries refresh the agent row -- an agent that was upgraded or
// renamed since enrollment reports the truth here.
func (h *AgentHandler) handshake(ctx context.Context, conn *agentConn, agent db.Agent, agentID string) (proto.Hello, error) {
  var hello proto.Hello

  hctx, cancel := context.WithTimeout(ctx, helloTimeout)
  defer cancel()

  env, err := readEnvelope(hctx, conn.ws)
  if err != nil {
    return hello, err
  }
  if env.Type != proto.TypeHello {
    return hello, errors.New("expected hello, got " + string(env.Type))
  }

  if err := json.Unmarshal(env.Payload, &hello); err != nil {
    return hello, err
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
    return hello, err
  }
  // Straight to this connection, not via the hub: if a replacement socket has
  // already displaced us, the ack must not go to it.
  return hello, conn.enqueue(ack)
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
  // A ruleset push settles no row, so it carries no correlation id and must be
  // answered before the id is parsed. There is nothing to record either: the
  // database already holds the policy, and this only says whether the host has
  // caught up with it yet.
  if res.Kind == proto.KindSuricataRules {
    if !res.OK {
      log.Printf("agent %s: could not apply the suricata ruleset: %s", agentID, res.Error)
    }
    return
  }
  // A proxy config push settles no row either, for the same reasons.
  if res.Kind == proto.KindProxyConfig {
    if !res.OK {
      log.Printf("agent %s: could not apply the proxy config: %s", agentID, res.Error)
    }
    return
  }
  // The two whole-file config pushes, same again.
  if res.Kind == proto.KindSuricataConfig || res.Kind == proto.KindDpipeConfig {
    if !res.OK {
      log.Printf("agent %s: could not apply the %s config: %s", agentID, res.Kind, res.Error)
    }
    return
  }
  // Same for vector: the settings are already stored, and this only says
  // whether the host has managed to install and start it.
  if res.Kind == proto.KindVectorConfig {
    if !res.OK {
      log.Printf("agent %s: could not apply the vector config: %s", agentID, res.Error)
    }
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
    // The address this VM held goes back to the pool and will be handed to some
    // other guest. Its pass rules have to be gone before that happens, or the
    // new guest inherits an allowlist it was never granted.
    // The proxy entry has to go for the same reason and with the same urgency:
    // until it does, the destroyed VM's owner still has a route pointing at an
    // address that is about to belong to somebody else's guest.
    if res.OK {
      pushSuricataRules(ctx, h.q, h.hub, agent.ID)
      pushProxyConfig(ctx, h.q, h.hub, h.proxy, agent.ID)
    }
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
  // The name the host reports back is deliberately not applied: it was chosen
  // here, it is unique across the fleet, and it is what the VM's http route is
  // keyed on.
  if err := qtx.MarkVMRunning(ctx, db.MarkVMRunningParams{
    ID:        rowID,
    VMID:      res.VM.ID,
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

  // Here rather than at the create request: the VM's address is allocated on the
  // host and is not knowable until this frame, and an entry with no target to
  // point at is not an entry. After the commit, so the generator reads the row
  // this result just wrote.
  pushProxyConfig(ctx, h.q, h.hub, h.proxy, agent.ID)
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

    // The host's name is only a suggestion here: it was chosen on a machine that
    // cannot see the rest of the fleet, so it may be taken or may not be a name
    // at all, and either way a generated one is used instead. The name only
    // applies if this turns out to be an insert -- the upsert leaves an existing
    // row's name alone, so the name a guest is reachable at does not move on
    // every tick.
    preferred, _ := validateVMName(v.Name)
    if err := withAdoptedVMName(ctx, preferred, func(ctx context.Context, name string) error {
      return h.q.UpsertVMFromInventory(ctx, db.UpsertVMFromInventoryParams{
        AgentID:   agent.ID,
        VMID:      v.ID,
        Name:      name,
        Status:    status,
        Boot:      v.Boot,
        CPUs:      int32(v.CPUs),
        MemoryMiB: int32(v.MemoryMiB),
        IP:        v.IP,
        CreatedAt: created,
        StartedAt: started,
      })
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

  // An inventory report is the only way this server hears about a VM destroyed
  // on the host directly, and a route left pointing at its address after it is
  // reissued would hand one user's key to another user's guest. Sent on every
  // tick rather than only when the report changed something: the agent restarts
  // proxy only when the file it receives differs from the one on disk, so an
  // unchanged fleet costs a frame and a comparison.
  pushProxyConfig(ctx, h.q, h.hub, h.proxy, agent.ID)
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
