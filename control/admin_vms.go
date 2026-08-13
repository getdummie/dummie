package main

import (
  "context"
  "encoding/json"
  "errors"
  "log"
  "net/http"
  "time"

  "github.com/google/uuid"
  "github.com/jackc/pgx/v5"
  "github.com/jackc/pgx/v5/pgtype"
  "github.com/labstack/echo/v5"

  "control/internal/db"
  "control/internal/proto"
)

// Creating a VM is a request to a machine we do not control the timing of: the
// agent downloads images and builds a filesystem, which takes minutes. So the
// endpoint records the intent, pushes the job, and answers 202 with the row.
// The row's status is the whole answer to "did it work" -- polling it is the
// intended use, not a workaround for a missing synchronous API.

type vmDTO struct {
  ID        string `json:"id"`
  AgentID   string `json:"agent_id"`
  VMID      string `json:"vm_id"`
  Name      string `json:"name"`
  Status    string `json:"status"` // pending | running | failed
  Boot      string `json:"boot"`
  CPUs      int32  `json:"cpus"`
  MemoryMiB int32  `json:"memory_mib"`
  DiskMiB   int32  `json:"disk_mib"`
  IP        string `json:"ip"`

  // The routing the proxy config is generated from: where a request goes when
  // nothing picks a port, and every port the VM publishes.
  DefaultPort int32   `json:"default_port"`
  PublicPorts []int32 `json:"public_ports"`

  Spec      json.RawMessage `json:"spec"`
  LastError string          `json:"last_error"`
  CreatedAt string          `json:"created_at"`
  StartedAt string          `json:"started_at"`

  // CreatedBy is "" for a VM nobody here asked for -- one adopted from an
  // agent's inventory report, or created before ownership was recorded.
  CreatedBy string `json:"created_by"`

  // URL is where the VM answers http, when a caller asked for a view that
  // resolves it. "" when the host has no domain, or when the endpoint does not
  // work it out -- the list endpoints do not, since resolving it per row would
  // be a query per VM.
  URL string `json:"url"`

  // ConsoleURL is the websocket endpoint the browser terminal talks to, filled
  // in on the same views that fill in URL and empty under the same conditions.
  // It is not a credential: opening it needs a token from POST
  // /vms/:id/console-token, which is minted per user and expires.
  ConsoleURL string `json:"console_url"`

  // ReportedAt is when the host last confirmed this VM; "" means it never has.
  // 'running' is the host's claim as of that moment, not a live observation, so
  // a reader has to weigh the status against this timestamp -- a status of
  // 'running' with a stale ReportedAt means "was running when last seen".
  ReportedAt string `json:"reported_at"`
}

func toVMDTO(v db.Vm) vmDTO {
  d := vmDTO{
    ID:          uuid.UUID(v.ID.Bytes).String(),
    AgentID:     uuid.UUID(v.AgentID.Bytes).String(),
    VMID:        v.VMID,
    Name:        v.Name,
    Status:      v.Status,
    Boot:        v.Boot,
    CPUs:        v.CPUs,
    MemoryMiB:   v.MemoryMiB,
    DiskMiB:     v.DiskMiB,
    IP:          v.IP,
    DefaultPort: v.DefaultPort,
    PublicPorts: v.PublicPorts,
    Spec:        json.RawMessage(v.Spec),
    LastError:   v.LastError,
    CreatedAt:   v.CreatedAt.Time.Format(time.RFC3339),
  }
  if len(d.Spec) == 0 {
    d.Spec = json.RawMessage("{}")
  }
  // An empty list, not null: a VM that publishes nothing is a real state, and
  // every reader of this field iterates it.
  if d.PublicPorts == nil {
    d.PublicPorts = []int32{}
  }
  if v.StartedAt.Valid {
    d.StartedAt = v.StartedAt.Time.Format(time.RFC3339)
  }
  if v.ReportedAt.Valid {
    d.ReportedAt = v.ReportedAt.Time.Format(time.RFC3339)
  }
  if v.CreatedBy.Valid {
    d.CreatedBy = uuid.UUID(v.CreatedBy.Bytes).String()
  }
  return d
}

func (h *AdminHandler) ListVMs(c *echo.Context) error {
  ctx := c.Request().Context()
  limit, offset := pageParams(c)
  total, err := h.q.CountVMs(ctx)
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not count vms")
  }
  rows, err := h.q.ListVMs(ctx, db.ListVMsParams{Limit: limit, Offset: offset})
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not list vms")
  }
  items := make([]vmDTO, 0, len(rows))
  for _, v := range rows {
    items = append(items, toVMDTO(v))
  }
  return c.JSON(http.StatusOK, pageEnvelope(items, total, limit, offset))
}

func (h *AdminHandler) ListAgentVMs(c *echo.Context) error {
  pgID, err := parseUUID(c.Param("id"))
  if err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, "invalid agent id")
  }
  ctx := c.Request().Context()
  limit, offset := pageParams(c)
  total, err := h.q.CountVMsByAgent(ctx, pgID)
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not count vms")
  }
  rows, err := h.q.ListVMsByAgent(ctx, db.ListVMsByAgentParams{
    AgentID: pgID, Limit: limit, Offset: offset,
  })
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not list vms")
  }
  items := make([]vmDTO, 0, len(rows))
  for _, v := range rows {
    items = append(items, toVMDTO(v))
  }
  return c.JSON(http.StatusOK, pageEnvelope(items, total, limit, offset))
}

func (h *AdminHandler) GetVM(c *echo.Context) error {
  pgID, err := parseUUID(c.Param("id"))
  if err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, "invalid vm id")
  }
  v, err := h.q.GetVM(c.Request().Context(), pgID)
  if err != nil {
    if errors.Is(err, pgx.ErrNoRows) {
      return echo.NewHTTPError(http.StatusNotFound, "no such vm")
    }
    return echo.NewHTTPError(http.StatusInternalServerError, "could not read vm")
  }
  return c.JSON(http.StatusOK, toVMDTO(v))
}

// ListVMTargets is the admin's view of what a VM is allowed to reach. Read-only
// on purpose: the list is the owner's own declaration of what their VM needs,
// and nothing enforces it yet, so an admin quietly editing it would leave the
// owner looking at a list they did not write. Seeing it is what an admin needs
// -- to answer why a VM is reaching somewhere, or what it will need when this
// does start being enforced.
func (h *AdminHandler) ListVMTargets(c *echo.Context) error {
  pgID, err := parseUUID(c.Param("id"))
  if err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, "invalid vm id")
  }
  ctx := c.Request().Context()
  // Read the VM first, so an id that matches nothing is a 404 rather than an
  // empty list that reads as "this VM is allowed nowhere".
  if _, err := h.q.GetVM(ctx, pgID); err != nil {
    if errors.Is(err, pgx.ErrNoRows) {
      return echo.NewHTTPError(http.StatusNotFound, "no such vm")
    }
    return echo.NewHTTPError(http.StatusInternalServerError, "could not read vm")
  }
  rows, err := h.q.ListVMNetworkTargets(ctx, pgID)
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not list destinations")
  }
  items := make([]vmTargetDTO, 0, len(rows))
  for _, t := range rows {
    items = append(items, toVMTargetDTO(t))
  }
  return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// adminCreateVMReq is the agent's own VM spec, inline, plus the routing the
// control plane owns. Embedded rather than nested so the body stays the spec
// `dagent vm create` accepts, with two more fields on it.
type adminCreateVMReq struct {
  proto.VMSpec
  DefaultPort int32   `json:"default_port"`
  PublicPorts []int32 `json:"public_ports"`
}

// CreateVM asks an agent to spin up a VM. The body is the agent's own VM spec,
// so anything `dagent vm create` accepts is accepted here too.
func (h *AdminHandler) CreateVM(c *echo.Context) error {
  agentID := c.Param("id")
  pgAgentID, err := parseUUID(agentID)
  if err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, "invalid agent id")
  }

  var req adminCreateVMReq
  if err := c.Bind(&req); err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
  }
  spec := req.VMSpec
  if spec.CPUs < 0 || spec.Memory < 0 {
    return echo.NewHTTPError(http.StatusBadRequest, "cpus and memory_mib cannot be negative")
  }
  // Held to the same rules as a user's: the name is the fleet-wide key an http
  // route is published under, so an admin does not get to write a duplicate or
  // an unroutable one either. Empty still means "generate one".
  name, err := validateVMName(spec.Name)
  if err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, err.Error())
  }
  defaultPort, publicPorts, err := normalizePorts(req.DefaultPort, req.PublicPorts)
  if err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, err.Error())
  }

  ctx := c.Request().Context()
  agent, err := h.q.GetAgentByID(ctx, pgAgentID)
  if err != nil {
    if errors.Is(err, pgx.ErrNoRows) {
      return echo.NewHTTPError(http.StatusNotFound, "no such agent")
    }
    return echo.NewHTTPError(http.StatusInternalServerError, "could not read agent")
  }
  if agent.Revoked {
    return echo.NewHTTPError(http.StatusConflict, "this agent has been revoked")
  }
  // Checked before the row is written so the common case -- a machine that is
  // simply not up -- is a clean rejection rather than a row that fails a moment
  // later. The race with a disconnect between here and Send is handled below.
  if !h.hub.Connected(agentID) {
    return echo.NewHTTPError(http.StatusConflict, "this agent is not connected")
  }

  // Best effort: an admin's spec is not bounded by a quota, so an unparseable
  // size is not worth refusing the create over -- it just does not count.
  diskMiB, _ := parseSizeMiB(spec.DiskSize)

  // Written before the job is pushed: a row with no job is a visible failure,
  // whereas a job with no row is a VM nobody knows about.
  params := db.CreateVMParams{
    AgentID:     pgAgentID,
    Boot:        spec.Boot,
    CPUs:        int32(spec.CPUs),
    MemoryMiB:   int32(spec.Memory),
    DiskMiB:     diskMiB,
    DefaultPort: defaultPort,
    PublicPorts: publicPorts,
  }
  // An admin creating a VM owns it like anyone else, so it shows up on their
  // own /vms and counts against their allowance.
  if uid, _ := c.Get("uid").(string); uid != "" {
    if pgID, err := parseUUID(uid); err == nil {
      params.CreatedBy = pgID
    }
  }
  // The name is settled by the insert, same as the user path: a generated one
  // that loses the race is redrawn, and only a name the caller chose comes back
  // as a conflict. The spec is encoded inside the loop so the host names the
  // guest whatever the row ended up holding.
  var row db.Vm
  if err := withVMName(ctx, name, func(ctx context.Context, name string) error {
    spec.Name = name
    raw, err := json.Marshal(spec)
    if err != nil {
      return err
    }
    params.Name, params.Spec = name, raw
    row, err = h.q.CreateVM(ctx, params)
    return err
  }); err != nil {
    if errors.Is(err, errVMNameTaken) {
      return echo.NewHTTPError(http.StatusConflict, "that name is already taken; choose another")
    }
    return echo.NewHTTPError(http.StatusInternalServerError, "could not record the vm")
  }
  rowID := uuid.UUID(row.ID.Bytes).String()

  // The row id is the correlation id, so the agent's result finds this row
  // without the server holding any in-flight state of its own.
  env, err := proto.NewEnvelope(proto.TypeJob, rowID, proto.Job{
    Kind: proto.KindVMCreate,
    VM:   &spec,
  })
  if err != nil {
    h.failVM(ctx, row.ID, "could not build the job frame")
    return echo.NewHTTPError(http.StatusInternalServerError, "could not build the job")
  }
  if err := h.hub.Send(agentID, env); err != nil {
    h.failVM(ctx, row.ID, "could not deliver the job: "+err.Error())
    return echo.NewHTTPError(http.StatusConflict, "could not deliver the job to this agent")
  }

  return c.JSON(http.StatusAccepted, toVMDTO(row))
}

// StopVM shuts the guest down but leaves everything it owns on the host, so
// StartVM can boot it again from the same disk at the same address.
func (h *AdminHandler) StopVM(c *echo.Context) error {
  return h.actOnVM(c, proto.KindVMStop)
}

// StartVM boots a VM that exists but is not running.
func (h *AdminHandler) StartVM(c *echo.Context) error {
  return h.actOnVM(c, proto.KindVMStart)
}

// DestroyVM stops the guest and deletes its disk and directory on the host. The
// row survives and becomes 'gone', because a VM that was destroyed is exactly
// what someone will want to look up afterwards.
func (h *AdminHandler) DestroyVM(c *echo.Context) error {
  return h.actOnVM(c, proto.KindVMDestroy)
}

// actOnVM pushes a job that names an existing VM. Both callers need the same
// four checks -- the row exists, the host assigned it an id, the agent is live,
// the frame was delivered -- so they share one implementation rather than two
// that drift.
func (h *AdminHandler) actOnVM(c *echo.Context, kind proto.JobKind) error {
  pgID, err := parseUUID(c.Param("id"))
  if err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, "invalid vm id")
  }

  ctx := c.Request().Context()
  row, err := h.q.GetVM(ctx, pgID)
  if err != nil {
    if errors.Is(err, pgx.ErrNoRows) {
      return echo.NewHTTPError(http.StatusNotFound, "no such vm")
    }
    return echo.NewHTTPError(http.StatusInternalServerError, "could not read vm")
  }
  // No local id means no host ever built it -- a pending or failed create. There
  // is nothing on any host to act on.
  if row.VMID == "" {
    return echo.NewHTTPError(http.StatusConflict, "this vm was never created on a host")
  }
  if row.Status == "gone" {
    return echo.NewHTTPError(http.StatusConflict, "this vm no longer exists on its host")
  }
  // The agent treats every action as idempotent, so these guards are about
  // telling the caller its request made no sense rather than about safety.
  // 'stale' is not checked: a row nobody has heard from recently is exactly one
  // an operator may need to act on.
  switch {
  case kind == proto.KindVMStart && row.Status == "running":
    return echo.NewHTTPError(http.StatusConflict, "this vm is already running")
  case kind == proto.KindVMStop && row.Status == "stopped":
    return echo.NewHTTPError(http.StatusConflict, "this vm is already stopped")
  }

  agentID := uuid.UUID(row.AgentID.Bytes).String()
  if !h.hub.Connected(agentID) {
    return echo.NewHTTPError(http.StatusConflict, "the host running this vm is not connected")
  }

  // The row id is the correlation id, exactly as for a create, so the result
  // settles this row without the server tracking in-flight jobs.
  env, err := proto.NewEnvelope(proto.TypeJob, uuid.UUID(row.ID.Bytes).String(), proto.Job{
    Kind: kind,
    VMID: row.VMID,
  })
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not build the job")
  }
  if err := h.hub.Send(agentID, env); err != nil {
    return echo.NewHTTPError(http.StatusConflict, "could not deliver the job to the host")
  }

  // 202: the guest is given time to shut down cleanly, so the row settles when
  // the agent reports back rather than by the time this returns.
  return c.JSON(http.StatusAccepted, toVMDTO(row))
}

// DeleteVM forgets the row. It does not touch the guest -- DestroyVM is what
// does that -- so a still-running VM is re-adopted by its host's next inventory
// report. Refusing here would be worse: it is the only way to clear the record
// of a host that is never coming back.
func (h *AdminHandler) DeleteVM(c *echo.Context) error {
  pgID, err := parseUUID(c.Param("id"))
  if err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, "invalid vm id")
  }
  if err := h.q.DeleteVM(c.Request().Context(), pgID); err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not delete vm")
  }
  return c.NoContent(http.StatusNoContent)
}

func (h *AdminHandler) failVM(ctx context.Context, id pgtype.UUID, msg string) {
  if err := h.q.MarkVMFailed(ctx, db.MarkVMFailedParams{ID: id, LastError: msg}); err != nil {
    log.Printf("could not mark vm failed: %v", err)
  }
}
