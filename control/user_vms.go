package main

import (
  "context"
  "encoding/json"
  "errors"
  "fmt"
  "log"
  "net/http"
  "regexp"
  "strings"

  "github.com/google/uuid"
  "github.com/jackc/pgx/v5"
  "github.com/jackc/pgx/v5/pgtype"
  "github.com/labstack/echo/v5"

  "control/internal/db"
  "control/internal/proto"
)

// UserHandler serves the self-service endpoints under /api/v1/vms. Everything
// here is scoped to the caller: the JWT middleware proves who is asking, and
// each query filters on created_by so one user's id is never enough to reach
// another user's row.
type UserHandler struct {
  q   *db.Queries
  hub *Hub
}

// callerID reads the uid the JWT middleware put on the context. A route behind
// userJWT always has one, so a miss is a wiring bug rather than a bad request.
func callerID(c *echo.Context) (pgtype.UUID, error) {
  uid, _ := c.Get("uid").(string)
  if uid == "" {
    return pgtype.UUID{}, errors.New("no caller id on the request")
  }
  return parseUUID(uid)
}

func (h *UserHandler) ListVMs(c *echo.Context) error {
  owner, err := callerID(c)
  if err != nil {
    return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
  }
  ctx := c.Request().Context()
  limit, offset := pageParams(c)
  total, err := h.q.CountVMsByOwner(ctx, owner)
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not count vms")
  }
  rows, err := h.q.ListVMsByOwner(ctx, db.ListVMsByOwnerParams{
    CreatedBy: owner, Limit: limit, Offset: offset,
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

func (h *UserHandler) GetVM(c *echo.Context) error {
  owner, err := callerID(c)
  if err != nil {
    return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
  }
  pgID, err := parseUUID(c.Param("id"))
  if err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, "invalid vm id")
  }
  v, err := h.q.GetVMForOwner(c.Request().Context(), db.GetVMForOwnerParams{
    ID: pgID, CreatedBy: owner,
  })
  if err != nil {
    if errors.Is(err, pgx.ErrNoRows) {
      // Someone else's VM and a VM that does not exist are the same answer, so
      // this cannot be used to discover which ids are real.
      return echo.NewHTTPError(http.StatusNotFound, "no such vm")
    }
    return echo.NewHTTPError(http.StatusInternalServerError, "could not read vm")
  }
  return c.JSON(http.StatusOK, toVMDTO(v))
}

// quotaDTO is what the create form needs to show a user where they stand before
// they fill anything in.
type quotaDTO struct {
  VCPULimit      int32 `json:"vcpu_limit"`
  MemoryLimitMiB int32 `json:"memory_limit_mib"`
  VCPUUsed       int32 `json:"vcpu_used"`
  MemoryUsedMiB  int32 `json:"memory_used_mib"`
}

func (h *UserHandler) GetQuota(c *echo.Context) error {
  owner, err := callerID(c)
  if err != nil {
    return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
  }
  ctx := c.Request().Context()
  u, err := h.q.GetUserByID(ctx, owner)
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not read your account")
  }
  used, err := h.q.SumActiveVMUsageByOwner(ctx, owner)
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not total your usage")
  }
  return c.JSON(http.StatusOK, quotaDTO{
    VCPULimit:      u.VCPULimit,
    MemoryLimitMiB: u.MemoryLimitMiB,
    VCPUUsed:       used.CPUs,
    MemoryUsedMiB:  used.MemoryMiB,
  })
}

// hostDTO is a host a self-service caller may target. Only the hostname is
// exposed -- the fleet's addresses, versions and metrics are not a user's
// business, and the id is needed only to name the choice on the way back.
type hostDTO struct {
  ID       string `json:"id"`
  Hostname string `json:"hostname"`
}

// ListHosts offers only agents with a live socket. An agent whose row still
// says 'online' but whose connection dropped would fail the create, so listing
// it is offering a choice that cannot work.
func (h *UserHandler) ListHosts(c *echo.Context) error {
  rows, err := h.q.ListAvailableHosts(c.Request().Context())
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not list hosts")
  }
  items := make([]hostDTO, 0, len(rows))
  for _, r := range rows {
    id := uuid.UUID(r.ID.Bytes).String()
    if !h.hub.Connected(id) {
      continue
    }
    items = append(items, hostDTO{ID: id, Hostname: r.Hostname})
  }
  return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// createVMReq is deliberately narrower than proto.VMSpec. A self-service caller
// gets the size, the two artifacts and their checksums; boot mode, addressing
// and egress policy are decided here, not by the request.
type createVMReq struct {
  AgentID   string `json:"agent_id"`
  Name      string `json:"name"`
  CPUs      int32  `json:"cpus"`
  MemoryMiB int32  `json:"memory_mib"`
  // DiskSize sizes the per-VM overlay. Deliberately not rootfs_size: that sizes
  // the base image, which is cached by the tar's digest alone and shared by
  // every VM built from that tar -- so a per-user value there would be silently
  // ignored for everyone after the first.
  DiskSize     string `json:"disk_size"`
  Kernel       string `json:"kernel"`
  KernelSHA    string `json:"kernel_sha256"`
  RootfsTar    string `json:"rootfs_tar"`
  RootfsTarSHA string `json:"rootfs_tar_sha256"`
}

// sizePattern matches what the agent's parseSize accepts: plain bytes or a
// single K/M/G/T suffix. Checked here so a typo is an immediate 400 rather than
// a failed row a minute later.
var sizePattern = regexp.MustCompile(`^[0-9]+[KkMmGgTt]?$`)

// sha256Pattern is a bare 64-character hex digest, which is the form the agent
// compares against.
var sha256Pattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

func (h *UserHandler) CreateVM(c *echo.Context) error {
  owner, err := callerID(c)
  if err != nil {
    return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
  }

  var req createVMReq
  if err := c.Bind(&req); err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
  }
  req.Name = strings.TrimSpace(req.Name)
  req.DiskSize = strings.TrimSpace(req.DiskSize)
  req.Kernel = strings.TrimSpace(req.Kernel)
  req.KernelSHA = strings.TrimSpace(req.KernelSHA)
  req.RootfsTar = strings.TrimSpace(req.RootfsTar)
  req.RootfsTarSHA = strings.TrimSpace(req.RootfsTarSHA)

  if req.CPUs < 1 {
    return echo.NewHTTPError(http.StatusBadRequest, "cpus must be at least 1")
  }
  if req.MemoryMiB < 64 {
    return echo.NewHTTPError(http.StatusBadRequest, "memory_mib must be at least 64")
  }
  if req.Kernel == "" {
    return echo.NewHTTPError(http.StatusBadRequest, "a kernel url is required")
  }
  if req.RootfsTar == "" {
    return echo.NewHTTPError(http.StatusBadRequest, "a root filesystem tar url is required")
  }
  if req.DiskSize != "" && !sizePattern.MatchString(req.DiskSize) {
    return echo.NewHTTPError(http.StatusBadRequest, "disk size must be a number, optionally with a K, M, G or T suffix")
  }
  if req.KernelSHA != "" && !sha256Pattern.MatchString(req.KernelSHA) {
    return echo.NewHTTPError(http.StatusBadRequest, "the kernel sha256 must be 64 hex characters")
  }
  if req.RootfsTarSHA != "" && !sha256Pattern.MatchString(req.RootfsTarSHA) {
    return echo.NewHTTPError(http.StatusBadRequest, "the root filesystem tar sha256 must be 64 hex characters")
  }

  ctx := c.Request().Context()

  // --- allowance -----------------------------------------------------------
  //
  // The limit is on the total a user holds at once, not on the size of any one
  // VM: a per-VM check would let someone create an unbounded number of
  // just-under-the-line VMs, which is not a limit.
  //
  // This is a read-then-write, so two creates racing can both see room for the
  // last slot. Closing that means taking a lock on the user row for the whole
  // create, which costs more than the overshoot is worth while nothing enforces
  // these numbers on the host anyway. Revisit alongside real enforcement.
  u, err := h.q.GetUserByID(ctx, owner)
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not read your account")
  }
  used, err := h.q.SumActiveVMUsageByOwner(ctx, owner)
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not total your usage")
  }
  if used.CPUs+req.CPUs > u.VCPULimit {
    return echo.NewHTTPError(http.StatusForbidden, fmt.Sprintf(
      "this would use %d vCPU of your %d limit; you are already using %d",
      used.CPUs+req.CPUs, u.VCPULimit, used.CPUs))
  }
  if used.MemoryMiB+req.MemoryMiB > u.MemoryLimitMiB {
    return echo.NewHTTPError(http.StatusForbidden, fmt.Sprintf(
      "this would use %d MiB of your %d MiB limit; you are already using %d MiB",
      used.MemoryMiB+req.MemoryMiB, u.MemoryLimitMiB, used.MemoryMiB))
  }

  // --- host ----------------------------------------------------------------
  //
  // Re-checked rather than trusted: the list the form was built from is a
  // snapshot, and the caller could name any agent id regardless of what was
  // offered.
  agentID := strings.TrimSpace(req.AgentID)
  pgAgentID, err := parseUUID(agentID)
  if err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, "choose a host to run this vm on")
  }
  agent, err := h.q.GetAgentByID(ctx, pgAgentID)
  if err != nil {
    if errors.Is(err, pgx.ErrNoRows) {
      return echo.NewHTTPError(http.StatusNotFound, "no such host")
    }
    return echo.NewHTTPError(http.StatusInternalServerError, "could not read host")
  }
  if agent.Revoked {
    return echo.NewHTTPError(http.StatusConflict, "that host has been revoked")
  }
  if !h.hub.Connected(agentID) {
    return echo.NewHTTPError(http.StatusConflict, "that host is not connected")
  }

  // Boot mode and egress are fixed for self-service creates: direct boot from a
  // kernel plus a tar-built rootfs, with unrestricted outbound. A user chooses
  // the artifacts and the size, not the policy.
  spec := proto.VMSpec{
    Name:         req.Name,
    Boot:         "direct",
    Kernel:       req.Kernel,
    KernelSHA:    req.KernelSHA,
    RootfsTar:    req.RootfsTar,
    RootfsTarSHA: req.RootfsTarSHA,
    DiskSize:     req.DiskSize,
    CPUs:         int(req.CPUs),
    Memory:       int(req.MemoryMiB),
    EgressAny:    true,
  }
  raw, err := json.Marshal(spec)
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not encode the vm spec")
  }

  // Written before the job is pushed, same as the admin path: a row with no job
  // is a visible failure, a job with no row is a VM nobody knows about.
  row, err := h.q.CreateVM(ctx, db.CreateVMParams{
    AgentID:   pgAgentID,
    Name:      spec.Name,
    Boot:      spec.Boot,
    CPUs:      req.CPUs,
    MemoryMiB: req.MemoryMiB,
    Spec:      raw,
    CreatedBy: owner,
  })
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not record the vm")
  }
  rowID := uuid.UUID(row.ID.Bytes).String()

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
    return echo.NewHTTPError(http.StatusConflict, "could not deliver the job to a host")
  }

  return c.JSON(http.StatusAccepted, toVMDTO(row))
}

// DestroyVM stops the guest and deletes its disk on the host. This is the only
// destructive action a self-service caller gets, and it is the one that frees
// the allowance the VM is holding.
func (h *UserHandler) DestroyVM(c *echo.Context) error {
  owner, err := callerID(c)
  if err != nil {
    return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
  }
  pgID, err := parseUUID(c.Param("id"))
  if err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, "invalid vm id")
  }

  ctx := c.Request().Context()
  row, err := h.q.GetVMForOwner(ctx, db.GetVMForOwnerParams{ID: pgID, CreatedBy: owner})
  if err != nil {
    if errors.Is(err, pgx.ErrNoRows) {
      return echo.NewHTTPError(http.StatusNotFound, "no such vm")
    }
    return echo.NewHTTPError(http.StatusInternalServerError, "could not read vm")
  }
  if row.VMID == "" {
    return echo.NewHTTPError(http.StatusConflict, "this vm was never created on a host")
  }
  if row.Status == "gone" {
    return echo.NewHTTPError(http.StatusConflict, "this vm no longer exists on its host")
  }

  agentID := uuid.UUID(row.AgentID.Bytes).String()
  if !h.hub.Connected(agentID) {
    return echo.NewHTTPError(http.StatusConflict, "the host running this vm is not connected")
  }
  env, err := proto.NewEnvelope(proto.TypeJob, uuid.UUID(row.ID.Bytes).String(), proto.Job{
    Kind: proto.KindVMDestroy,
    VMID: row.VMID,
  })
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not build the job")
  }
  if err := h.hub.Send(agentID, env); err != nil {
    return echo.NewHTTPError(http.StatusConflict, "could not deliver the job to the host")
  }

  // 202: the row settles when the agent reports back, not by the time this
  // returns.
  return c.JSON(http.StatusAccepted, toVMDTO(row))
}

func (h *UserHandler) failVM(ctx context.Context, id pgtype.UUID, msg string) {
  if err := h.q.MarkVMFailed(ctx, db.MarkVMFailedParams{ID: id, LastError: msg}); err != nil {
    log.Printf("could not mark vm failed: %v", err)
  }
}
