package main

import (
  "context"
  "encoding/json"
  "errors"
  "fmt"
  "log"
  "net"
  "net/http"
  "regexp"
  "strconv"
  "strings"
  "time"

  "github.com/google/uuid"
  "github.com/jackc/pgx/v5"
  "github.com/jackc/pgx/v5/pgconn"
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
  DiskLimitMiB   int32 `json:"disk_limit_mib"`
  VCPUUsed       int32 `json:"vcpu_used"`
  MemoryUsedMiB  int32 `json:"memory_used_mib"`
  DiskUsedMiB    int32 `json:"disk_used_mib"`
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
    DiskLimitMiB:   u.DiskLimitMiB,
    VCPUUsed:       used.CPUs,
    MemoryUsedMiB:  used.MemoryMiB,
    DiskUsedMiB:    used.DiskMiB,
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

// parseSizeMiB converts the agent's size syntax to MiB, so a disk size given as
// "2G" can be totalled against a limit stored as a number. Mirrors the agent's
// own parseSize (cmd/dagent/vm.go) -- same units, same suffixes.
//
// Rounds up: a 1.5 GiB disk that counted as 1 GiB would let a user hold more
// than their limit, and rounding a limit check in the user's favour is the
// wrong direction to be imprecise in. An empty string is 0, matching the agent,
// where an unset size means "no explicit size".
func parseSizeMiB(s string) (int32, error) {
  s = strings.TrimSpace(s)
  if s == "" {
    return 0, nil
  }
  mult := int64(1)
  switch unit := s[len(s)-1]; unit {
  case 'K', 'k':
    mult = 1 << 10
  case 'M', 'm':
    mult = 1 << 20
  case 'G', 'g':
    mult = 1 << 30
  case 'T', 't':
    mult = 1 << 40
  default:
    if unit < '0' || unit > '9' {
      return 0, fmt.Errorf("unknown size unit %q", string(unit))
    }
  }
  if mult > 1 {
    s = s[:len(s)-1]
  }
  n, err := strconv.ParseInt(s, 10, 64)
  if err != nil || n < 0 {
    return 0, fmt.Errorf("invalid size %q", s)
  }
  bytes := n * mult
  const mib = 1 << 20
  return int32((bytes + mib - 1) / mib), nil
}

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
  diskMiB, err := parseSizeMiB(req.DiskSize)
  if err != nil {
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
      "this would use %d MiB of memory against your %d MiB limit; you are already using %d MiB",
      used.MemoryMiB+req.MemoryMiB, u.MemoryLimitMiB, used.MemoryMiB))
  }
  if used.DiskMiB+diskMiB > u.DiskLimitMiB {
    return echo.NewHTTPError(http.StatusForbidden, fmt.Sprintf(
      "this would use %d MiB of disk against your %d MiB limit; you are already using %d MiB",
      used.DiskMiB+diskMiB, u.DiskLimitMiB, used.DiskMiB))
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
    DiskMiB:   diskMiB,
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

// StartVM boots a VM that exists but is not running.
func (h *UserHandler) StartVM(c *echo.Context) error {
  return h.actOnVM(c, proto.KindVMStart)
}

// StopVM shuts the guest down but leaves its disk on the host, so StartVM can
// boot it again from the same state. The allowance it holds is NOT freed: a
// stopped VM still owns its disk and its slot, and letting a stop free the quota
// would make the limit trivially evadable by stopping and creating in a loop.
func (h *UserHandler) StopVM(c *echo.Context) error {
  return h.actOnVM(c, proto.KindVMStop)
}

// DestroyVM stops the guest and deletes its disk on the host. This is the one
// action that frees the allowance the VM is holding.
func (h *UserHandler) DestroyVM(c *echo.Context) error {
  return h.actOnVM(c, proto.KindVMDestroy)
}

// actOnVM pushes a job naming an existing VM the caller owns. All three actions
// need the same checks -- the row is theirs, the host assigned it an id, the
// agent is live, the frame was delivered -- so they share one implementation
// rather than three that drift.
func (h *UserHandler) actOnVM(c *echo.Context, kind proto.JobKind) error {
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
  // The agent treats every action as idempotent, so these guards are about
  // telling the caller its request made no sense rather than about safety.
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

  // 202: the row settles when the agent reports back, not by the time this
  // returns.
  return c.JSON(http.StatusAccepted, toVMDTO(row))
}

// --- network targets --------------------------------------------------------

type vmTargetDTO struct {
  ID          string `json:"id"`
  Destination string `json:"destination"`
  Kind        string `json:"kind"`      // domain | ip
  Transport   string `json:"transport"` // ip rows only: tcp | udp | any
  Ports       string `json:"ports"`     // ip rows only; "" = any
  Note        string `json:"note"`
  CreatedAt   string `json:"created_at"`
}

func toVMTargetDTO(t db.VmNetworkTarget) vmTargetDTO {
  return vmTargetDTO{
    ID:          uuid.UUID(t.ID.Bytes).String(),
    Destination: t.Destination,
    Kind:        t.Kind,
    Transport:   t.Transport,
    Ports:       t.Ports,
    Note:        t.Note,
    CreatedAt:   t.CreatedAt.Time.Format(time.RFC3339),
  }
}

// ownedVM resolves the VM in the path and proves the caller owns it. Every
// target route starts here, so none of them can operate on a VM by id alone.
func (h *UserHandler) ownedVM(c *echo.Context) (db.Vm, error) {
  owner, err := callerID(c)
  if err != nil {
    return db.Vm{}, echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
  }
  pgID, err := parseUUID(c.Param("id"))
  if err != nil {
    return db.Vm{}, echo.NewHTTPError(http.StatusBadRequest, "invalid vm id")
  }
  row, err := h.q.GetVMForOwner(c.Request().Context(), db.GetVMForOwnerParams{
    ID: pgID, CreatedBy: owner,
  })
  if err != nil {
    if errors.Is(err, pgx.ErrNoRows) {
      return db.Vm{}, echo.NewHTTPError(http.StatusNotFound, "no such vm")
    }
    return db.Vm{}, echo.NewHTTPError(http.StatusInternalServerError, "could not read vm")
  }
  return row, nil
}

func (h *UserHandler) ListTargets(c *echo.Context) error {
  vm, err := h.ownedVM(c)
  if err != nil {
    return err
  }
  rows, err := h.q.ListVMNetworkTargets(c.Request().Context(), vm.ID)
  if err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not list destinations")
  }
  items := make([]vmTargetDTO, 0, len(rows))
  for _, t := range rows {
    items = append(items, toVMTargetDTO(t))
  }
  return c.JSON(http.StatusOK, map[string]any{"items": items})
}

type createTargetReq struct {
  // Kind is what the caller says this is. Optional: omitted, the destination is
  // classified for them. Supplied and disagreeing with the destination, the
  // request is rejected -- a form that asked for an address and got a hostname
  // has a mistake in it, and quietly storing the other kind hides it.
  Kind        string `json:"kind"`
  Destination string `json:"destination"`
  // Both ignored when the destination is a domain: a domain compiles to
  // dns.query / tls.sni / http.host rules whose headers are `any any`, so there
  // is nowhere to put either one.
  Transport string `json:"transport"`
  Ports     string `json:"ports"`
  Note      string `json:"note"`
}

// portsPattern is Suricata's port syntax, restricted to the forms worth
// offering: a single port, a comma-separated list, or a colon range. Validated
// rather than passed through, because this string ends up inside a generated
// rule and a malformed one breaks the whole ruleset, not just this line.
var portsPattern = regexp.MustCompile(`^[0-9]+(:[0-9]+)?(,[0-9]+(:[0-9]+)?)*$`)

// hostPattern is a conservative hostname: labels of alphanumerics and hyphens,
// at least two of them. Wildcards are not accepted -- a leading '*' means
// something specific in a rule generator and is worth adding deliberately.
var hostPattern = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)+$`)

// classifyDestination decides whether a destination is an address or a name,
// and rejects anything that is neither. Stored rather than re-derived so the
// rule generator does not have to repeat this and reach a different answer.
func classifyDestination(s string) (string, error) {
  if _, _, err := net.ParseCIDR(s); err == nil {
    return "ip", nil
  }
  if net.ParseIP(s) != nil {
    return "ip", nil
  }
  if hostPattern.MatchString(s) {
    return "domain", nil
  }
  return "", errors.New("destination must be a domain, an IP address, or a CIDR")
}

func (h *UserHandler) CreateTarget(c *echo.Context) error {
  vm, err := h.ownedVM(c)
  if err != nil {
    return err
  }

  var req createTargetReq
  if err := c.Bind(&req); err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
  }
  req.Destination = strings.TrimSpace(req.Destination)
  req.Ports = strings.ReplaceAll(strings.TrimSpace(req.Ports), " ", "")
  req.Note = strings.TrimSpace(req.Note)
  req.Transport = strings.ToLower(strings.TrimSpace(req.Transport))

  if req.Destination == "" {
    return echo.NewHTTPError(http.StatusBadRequest, "a destination is required")
  }
  kind, err := classifyDestination(req.Destination)
  if err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, err.Error())
  }
  if declared := strings.ToLower(strings.TrimSpace(req.Kind)); declared != "" && declared != kind {
    switch declared {
    case "domain":
      return echo.NewHTTPError(http.StatusBadRequest, "that is an address, not a domain")
    case "ip":
      return echo.NewHTTPError(http.StatusBadRequest, "that is a domain, not an IP address or CIDR")
    default:
      return echo.NewHTTPError(http.StatusBadRequest, "type must be domain or ip")
    }
  }

  // The kind decides which of the remaining fields mean anything. Cleared
  // rather than rejected for a domain: the form hides them, so a stale value
  // arriving is this server's problem to normalise, not the caller's to fix.
  if kind == "domain" {
    req.Transport, req.Ports = "", ""
    // A hostname is matched case-insensitively against buffers Suricata
    // normalises to lowercase, so storing it lowercased keeps the unique index
    // from treating Example.com and example.com as two allowances.
    req.Destination = strings.ToLower(req.Destination)
  } else {
    if req.Transport == "" {
      req.Transport = "tcp"
    }
    switch req.Transport {
    case "tcp", "udp", "any":
    default:
      return echo.NewHTTPError(http.StatusBadRequest, "transport must be tcp, udp or any")
    }
    if req.Ports != "" {
      if !portsPattern.MatchString(req.Ports) {
        return echo.NewHTTPError(http.StatusBadRequest,
          "ports must be a port, a list like 80,443, or a range like 1000:2000")
      }
      for _, part := range strings.Split(strings.ReplaceAll(req.Ports, ":", ","), ",") {
        n, err := strconv.Atoi(part)
        if err != nil || n < 1 || n > 65535 {
          return echo.NewHTTPError(http.StatusBadRequest, "ports must be between 1 and 65535")
        }
      }
    }
  }

  t, err := h.q.CreateVMNetworkTarget(c.Request().Context(), db.CreateVMNetworkTargetParams{
    VMID:        vm.ID,
    Destination: req.Destination,
    Kind:        kind,
    Transport:   req.Transport,
    Ports:       req.Ports,
    Note:        req.Note,
  })
  if err != nil {
    var pgErr *pgconn.PgError
    if errors.As(err, &pgErr) && pgErr.Code == "23505" {
      return echo.NewHTTPError(http.StatusConflict, "that destination is already on the list")
    }
    return echo.NewHTTPError(http.StatusInternalServerError, "could not add the destination")
  }
  // Whole-host, not whole-VM: the ruleset is one file covering every guest, so
  // it is regenerated from the database rather than patched with this row.
  pushSuricataRules(c.Request().Context(), h.q, h.hub, vm.AgentID)
  return c.JSON(http.StatusCreated, toVMTargetDTO(t))
}

func (h *UserHandler) DeleteTarget(c *echo.Context) error {
  vm, err := h.ownedVM(c)
  if err != nil {
    return err
  }
  targetID, err := parseUUID(c.Param("target_id"))
  if err != nil {
    return echo.NewHTTPError(http.StatusBadRequest, "invalid destination id")
  }
  if err := h.q.DeleteVMNetworkTarget(c.Request().Context(), db.DeleteVMNetworkTargetParams{
    ID: targetID, VMID: vm.ID,
  }); err != nil {
    return echo.NewHTTPError(http.StatusInternalServerError, "could not remove the destination")
  }
  // A removal has to reach the host even more urgently than an addition: until
  // it does, the guest still has the access the user just revoked.
  pushSuricataRules(c.Request().Context(), h.q, h.hub, vm.AgentID)
  return c.NoContent(http.StatusNoContent)
}

func (h *UserHandler) failVM(ctx context.Context, id pgtype.UUID, msg string) {
  if err := h.q.MarkVMFailed(ctx, db.MarkVMFailedParams{ID: id, LastError: msg}); err != nil {
    log.Printf("could not mark vm failed: %v", err)
  }
}
