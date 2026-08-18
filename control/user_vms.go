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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"control/internal/db"
	"control/internal/proto"
)

// UserHandler serves the self-service endpoints under /api/v1/vms. Everything
// here is scoped to the caller: the JWT middleware proves who is asking, and
// each query filters on created_by so one user's id is never enough to reach
// another user's row.
type UserHandler struct {
	q *db.Queries
	// pool is here for the one thing q cannot do: write a row and the scheduled
	// task that expires it in a single transaction. nil when no database is
	// configured, which the routes that need it report rather than panic on.
	pool *pgxpool.Pool
	hub  *Hub
	// prod picks the scheme for the links this hands out: a dev control plane is
	// served over http, and a link to https it does not answer on is worse than
	// no link at all.
	prod bool
	// proxy is carried only to be handed to pushProxyConfig.
	proxy proxyAuthConfig
	// blobs mints the download link a host fetches a chosen kernel from. nil when
	// no bucket is configured, which is what makes a create impossible to serve.
	blobs *blobStore
	// ch reads the suricata events clients ship. nil when no clickhouse is
	// configured, which the routes that use it report as "unavailable" rather
	// than as an empty result.
	ch driver.Conn
}

// vmURL is where a VM answers http: its name under the domain of the host it
// runs on, which is exactly the hostname the generated proxy config publishes
// it under. Worked out here rather than in the browser because both halves are
// server-side facts -- the client's domain is not on the VM row, and whether
// this deployment serves https is not something the client can see.
//
// "" when the host has no domain. That is the honest answer rather than a gap:
// without one there is no name to route on, and inventing a suffix would hand
// the user a link nothing resolves.
func (h *UserHandler) vmURL(ctx context.Context, v db.Vm) string {
	tld := h.vmDomainTLD(ctx, v)
	if tld == "" {
		return ""
	}
	scheme := "http"
	if h.prod {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s.%s", scheme, v.Name, tld)
}

// vmDomainTLD is the domain of the host a VM runs on, or "" when it has none or
// the VM has no name to sit under one. Split out of vmURL because the console
// hostname is built from the same two halves under a different shape.
func (h *UserHandler) vmDomainTLD(ctx context.Context, v db.Vm) string {
	if v.Name == "" {
		return ""
	}
	client, err := h.q.GetClientByID(ctx, v.ClientID)
	if err != nil || !client.DomainID.Valid {
		return ""
	}
	// No query reads a single domain by id, and an installation holds a handful,
	// so scanning them beats adding one for this lookup.
	domains, err := h.q.ListDomains(ctx)
	if err != nil {
		return ""
	}
	for _, d := range domains {
		if d.ID == client.DomainID {
			return d.TLD
		}
	}
	return ""
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

// ListVMs returns the caller's own VMs.
//
// @Summary     List your VMs
// @Description Scoped to you by the query itself. url and console_url are empty here: resolving them would be a query per row, so ask for a single VM when you need them.
// @Tags        vms
// @Produce     json
// @Security    BearerAuth
// @Param       limit  query int false "1-100" default(20)
// @Param       offset query int false "rows to skip" default(0)
// @Success     200 {object} pagedVMs
// @Failure     401 {object} apiError
// @Router      /vms [get]
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
	fillVMExpiries(ctx, h.q, items)
	return c.JSON(http.StatusOK, pageEnvelope(items, total, limit, offset))
}

// GetVM returns one of the caller's VMs.
//
// @Summary     Read one of your VMs
// @Description Someone else's VM and a VM that does not exist are the same 404, so this cannot be used to discover which ids are real. status is the host's claim as of reported_at, not a live observation.
// @Tags        vms
// @Produce     json
// @Security    BearerAuth
// @Param       id path string true "vm id" format(uuid)
// @Success     200 {object} vmDTO
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Router      /vms/{id} [get]
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
	ctx := c.Request().Context()
	items := []vmDTO{toVMDTO(v)}
	items[0].URL = h.vmURL(ctx, v)
	items[0].ConsoleURL = h.consoleURL(ctx, v)
	fillVMExpiries(ctx, h.q, items)
	return c.JSON(http.StatusOK, items[0])
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

// GetQuota reports the caller's allowance and how much of it is spent.
//
// @Summary     Read your quota
// @Description What you may hold across every VM at once, and what your active VMs already use. Check this before a create rather than discovering the refusal.
// @Tags        vms
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} quotaDTO
// @Failure     401 {object} apiError
// @Router      /vms/quota [get]
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

// ListHosts offers only clients with a live socket. An client whose row still
// says 'online' but whose connection dropped would fail the create, so listing
// it is offering a choice that cannot work.
// ListHosts offers the hosts a create may target.
//
// @Summary     List available hosts
// @Description Only hosts with a live socket to this server. One whose row still says 'online' but whose connection dropped would fail the create, so it is left out. The id is what you pass as client_id.
// @Tags        vms
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} hostList
// @Failure     401 {object} apiError
// @Router      /vms/hosts [get]
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

// userArtifactDTO is one catalogue entry -- a kernel or an OS image -- as the
// create form shows it. No object key and no download link: a user picks an
// entry, and the link a host fetches it from is minted server-side when the job
// is built.
type userArtifactDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	SizeBytes   int64  `json:"size_bytes"`
	CreatedAt   string `json:"created_at"`
}

// maxArtifactChoices bounds the list handed to the picker. Well above any real
// catalogue; it exists so the form is not asked to render an unbounded list.
const maxArtifactChoices = 100

// ListKernels offers the catalogue, newest first, to any signed-in caller.
// Withdrawn kernels are not in it: the query leaves them out, which is what
// stops a user choosing one the create would then refuse.
//
// @Summary     List available kernels
// @Description The catalogue an admin uploaded, newest first, withdrawn entries left out. No object key and no download link: you pick an entry, and the link the host fetches it from is minted server-side when the job is built. The id is what you pass as kernel_id.
// @Tags        vms
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} artifactList
// @Failure     401 {object} apiError
// @Router      /vms/kernels [get]
func (h *UserHandler) ListKernels(c *echo.Context) error {
	rows, err := h.q.ListKernels(c.Request().Context(), db.ListKernelsParams{Limit: maxArtifactChoices})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list kernels")
	}
	items := make([]userArtifactDTO, 0, len(rows))
	for _, k := range rows {
		items = append(items, userArtifactDTO{
			ID:          uuid.UUID(k.ID.Bytes).String(),
			Name:        k.Name,
			Description: k.Description,
			SizeBytes:   k.SizeBytes,
			CreatedAt:   k.CreatedAt.Time.Format(time.RFC3339),
		})
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// ListOSImages offers the OS image catalogue on the same terms as the kernel
// one: newest first, withdrawn entries left out, no keys or links.
//
// @Summary     List available OS images
// @Description The root-filesystem catalogue, on the same terms as the kernel one. The id is what you pass as osimage_id.
// @Tags        vms
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} artifactList
// @Failure     401 {object} apiError
// @Router      /vms/osimages [get]
func (h *UserHandler) ListOSImages(c *echo.Context) error {
	rows, err := h.q.ListOSImages(c.Request().Context(), db.ListOSImagesParams{Limit: maxArtifactChoices})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list os images")
	}
	items := make([]userArtifactDTO, 0, len(rows))
	for _, o := range rows {
		items = append(items, userArtifactDTO{
			ID:          uuid.UUID(o.ID.Bytes).String(),
			Name:        o.Name,
			Description: o.Description,
			SizeBytes:   o.SizeBytes,
			CreatedAt:   o.CreatedAt.Time.Format(time.RFC3339),
		})
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// createVMReq is deliberately narrower than proto.VMSpec. A self-service caller
// gets the size, the two artifacts and their checksums; boot mode, addressing
// and egress policy are decided here, not by the request.
type createVMReq struct {
	ClientID  string `json:"client_id"`
	Name      string `json:"name"`
	CPUs      int32  `json:"cpus"`
	MemoryMiB int32  `json:"memory_mib"`
	// DiskSize sizes the per-VM overlay. Deliberately not rootfs_size: that sizes
	// the base image, which is cached by the tar's digest alone and shared by
	// every VM built from that tar -- so a per-user value there would be silently
	// ignored for everyone after the first.
	DiskSize string `json:"disk_size"`
	// KernelID names a row in the kernels catalogue. A caller picks from what an
	// admin uploaded rather than supplying a url: the artifact a guest boots is
	// the installation's decision, and an arbitrary url would make every VM's
	// kernel a fetch from wherever its creator pointed.
	KernelID string `json:"kernel_id"`
	// OSImageID names a row in the OS images catalogue, and is the root
	// filesystem half of the same arrangement as KernelID.
	OSImageID string `json:"osimage_id"`

	// DefaultPort is where a request goes when nothing picks a port. 0 means
	// unset, and becomes defaultVMPort.
	DefaultPort int32 `json:"default_port"`
	// PublicPorts is every port the VM publishes. Empty publishes nothing.
	PublicPorts []int32 `json:"public_ports"`

	// TTLSeconds makes this a temporary sandbox: the control plane destroys the VM
	// this many seconds after the row is written. 0 is the ordinary case -- a VM
	// that lives until somebody destroys it.
	//
	// Measured from the create, and it keeps running while the VM is stopped. A
	// clock that paused on stop would make the TTL evadable by exactly the trick
	// the quota already refuses to reward, and what a temporary sandbox is
	// bounding is wall-clock exposure rather than uptime.
	TTLSeconds int64 `json:"ttl_seconds"`

	// Targets is the egress allowlist to give the VM at birth, in exactly the
	// shape POST /vms/{id}/targets takes one at a time. Empty is a VM that may
	// reach nothing until somebody allows something.
	//
	// Here rather than left to follow-up calls because the guest boots and starts
	// trying to reach things immediately: an allowlist applied a moment later is a
	// window in which the sandbox is already running and already denied, which
	// reads as a broken VM rather than as policy arriving.
	Targets []createTargetReq `json:"targets"`
}

// maxCreateTargets bounds the allowlist one create may carry. Well above any
// real starting policy; it is here because these rows are written in a single
// transaction, and an unbounded list would make one request hold it open for as
// long as it liked.
const maxCreateTargets = 32

// defaultVMPort is what a VM gets when the request does not name one. It is the
// column default too; repeated here so that a request that omits the field and
// one that sends 0 reach the same row.
const defaultVMPort = 8000

// maxPublicPorts bounds a list that goes into a generated file on every host.
// Well above any real use; it exists so one request cannot make every client's
// proxy config arbitrarily large.
const maxPublicPorts = 32

// normalizePorts settles the two port fields together: they are validated the
// same way, and the default is only meaningful next to the list.
//
// The list is de-duplicated but not sorted -- the order is the caller's, and it
// is the order the generated file lists them in, so reordering it would be a
// change the caller did not ask for that shows up in a diff on every host.
func normalizePorts(defaultPort int32, public []int32) (int32, []int32, error) {
	if defaultPort == 0 {
		defaultPort = defaultVMPort
	}
	if defaultPort < 1 || defaultPort > 65535 {
		return 0, nil, errors.New("default_port must be between 1 and 65535")
	}
	if len(public) > maxPublicPorts {
		return 0, nil, fmt.Errorf("a vm can publish at most %d ports", maxPublicPorts)
	}
	seen := make(map[int32]bool, len(public))
	ports := make([]int32, 0, len(public))
	for _, p := range public {
		if p < 1 || p > 65535 {
			return 0, nil, errors.New("every public port must be between 1 and 65535")
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		ports = append(ports, p)
	}
	return defaultPort, ports, nil
}

// sizePattern matches what the client's parseSize accepts: plain bytes or a
// single K/M/G/T suffix. Checked here so a typo is an immediate 400 rather than
// a failed row a minute later.
var sizePattern = regexp.MustCompile(`^[0-9]+[KkMmGgTt]?$`)

// sha256Pattern is a bare 64-character hex digest, which is the form the client
// compares against.
var sha256Pattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// parseSizeMiB converts the client's size syntax to MiB, so a disk size given as
// "2G" can be totalled against a limit stored as a number. Mirrors the client's
// own parseSize (cmd/dclient/vm.go) -- same units, same suffixes.
//
// Rounds up: a 1.5 GiB disk that counted as 1 GiB would let a user hold more
// than their limit, and rounding a limit check in the user's favour is the
// wrong direction to be imprecise in. An empty string is 0, matching the client,
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

// CreateVM builds one VM for the caller on a host of their choosing.
//
// @Summary     Create a VM
// @Description Pick a host from /vms/hosts and an artifact from /vms/kernels and /vms/osimages. Your SSH public key has to be on your account first -- it is built into the image at boot, so it cannot be added afterwards. The size is charged against your quota; disk_size takes the client's syntax ("2G", "512M", or plain bytes).
// @Description
// @Description Set ttl_seconds to make this a temporary sandbox: the control plane destroys it that many seconds after the row is written. The clock is wall-clock from the create and keeps running while the VM is stopped.
// @Description
// @Description targets is the egress allowlist to give the VM at birth — each entry takes exactly the shape `POST /vms/{id}/targets` accepts, including its own ttl_seconds. At most 32; add the rest afterwards. The whole list is validated before anything is written and inserted in the same transaction as the VM, so a bad entry is a 400 with no VM created rather than a VM with a partial allowlist. Omit it for a VM that may reach nothing until you allow something.
// @Description
// @Description The response is the row as written, with status 'pending'. The host reports the result over its own socket, so poll GET /vms/{id} to see it reach 'running' or 'failed'. The allowlist reaches the host with the guest's address, so it is in force by the time the VM is up.
// @Tags        vms
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       body body createVMReq true "client_id, kernel_id and osimage_id are required"
// @Success     202 {object} vmDTO "accepted and pending; the host has not reported yet"
// @Failure     400 {object} apiError "bad size, bad port count, cpus under 1, memory under 64 MiB, or a destination that is not allowable"
// @Failure     401 {object} apiError
// @Failure     403 {object} apiError "no public key on your account, or over quota"
// @Failure     404 {object} apiError "no such host, kernel or os image"
// @Failure     409 {object} apiError "host revoked or disconnected, artifact withdrawn, name taken, or the job could not be delivered"
// @Failure     502 {object} apiError "could not prepare an artifact download"
// @Failure     503 {object} apiError "no blob store is configured on this installation"
// @Router      /vms [post]
func (h *UserHandler) CreateVM(c *echo.Context) error {
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}

	var req createVMReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	// Empty is allowed and means "generate one": the name has to be unique across
	// the fleet, and that is not something to make a person guess at.
	name, err := validateVMName(req.Name)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	defaultPort, publicPorts, err := normalizePorts(req.DefaultPort, req.PublicPorts)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	ttl, err := parseTTLSeconds(req.TTLSeconds)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	req.DiskSize = strings.TrimSpace(req.DiskSize)
	req.KernelID = strings.TrimSpace(req.KernelID)
	req.OSImageID = strings.TrimSpace(req.OSImageID)

	if req.CPUs < 1 {
		return echo.NewHTTPError(http.StatusBadRequest, "cpus must be at least 1")
	}
	if req.MemoryMiB < 64 {
		return echo.NewHTTPError(http.StatusBadRequest, "memory_mib must be at least 64")
	}
	if req.KernelID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "a kernel is required")
	}
	pgKernelID, err := parseUUID(req.KernelID)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid kernel id")
	}
	if req.OSImageID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "an os image is required")
	}
	pgOSImageID, err := parseUUID(req.OSImageID)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid os image id")
	}
	if req.DiskSize != "" && !sizePattern.MatchString(req.DiskSize) {
		return echo.NewHTTPError(http.StatusBadRequest, "disk size must be a number, optionally with a K, M, G or T suffix")
	}
	diskMiB, err := parseSizeMiB(req.DiskSize)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "disk size must be a number, optionally with a K, M, G or T suffix")
	}

	// The allowlist is settled before anything is written, so a typo in the tenth
	// destination is a 400 rather than a VM that exists with nine of the ten
	// allowances its owner asked for.
	if len(req.Targets) > maxCreateTargets {
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf(
			"a vm can be created with at most %d destinations; add the rest afterwards", maxCreateTargets))
	}
	targets := make([]normalizedTarget, 0, len(req.Targets))
	// The unique index would catch a repeat, but only by aborting the transaction
	// that is also writing the VM -- so the same request asking for a destination
	// twice would lose the VM too. Caught here, where it is still just a typo.
	seenTargets := make(map[string]bool, len(req.Targets))
	for i, t := range req.Targets {
		nt, err := normalizeTarget(t)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf(
				"destination %d (%q): %s", i+1, strings.TrimSpace(t.Destination), err.Error()))
		}
		key := nt.params.Destination + "\x00" + nt.params.Transport + "\x00" + nt.params.Ports
		if seenTargets[key] {
			return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf(
				"%s is listed twice", nt.params.Destination))
		}
		seenTargets[key] = true
		targets = append(targets, nt)
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

	// A VM is only useful to someone who can get into it, and in these images the
	// key has to be in the root filesystem before it is turned into a disk -- there
	// is no cloud-init and no guest client, so adding one afterwards means a rebuild.
	// Refusing the create is the last point at which a missing key is cheap to fix.
	if u.PublicKey == "" {
		return echo.NewHTTPError(http.StatusForbidden,
			"add an SSH public key to your profile in Settings before creating a VM")
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
	// snapshot, and the caller could name any client id regardless of what was
	// offered.
	clientID := strings.TrimSpace(req.ClientID)
	pgClientID, err := parseUUID(clientID)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "choose a host to run this vm on")
	}
	client, err := h.q.GetClientByID(ctx, pgClientID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "no such host")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read host")
	}
	if client.Revoked {
		return echo.NewHTTPError(http.StatusConflict, "that host has been revoked")
	}
	if !h.hub.Connected(clientID) {
		return echo.NewHTTPError(http.StatusConflict, "that host is not connected")
	}

	// Both chosen artifacts become links the host can fetch. Minted here, at the
	// moment the job is built, because they expire: a link stored earlier and used
	// later is a create that fails for no reason the user can see.
	if h.blobs == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, errNoBlobStore.Error())
	}
	kernel, err := h.q.GetKernel(ctx, pgKernelID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "no such kernel")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the kernel")
	}
	if kernel.SoftDeletedAt.Valid {
		return echo.NewHTTPError(http.StatusConflict, "that kernel has been withdrawn; choose another")
	}
	osImage, err := h.q.GetOSImage(ctx, pgOSImageID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "no such os image")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the os image")
	}
	if osImage.SoftDeletedAt.Valid {
		return echo.NewHTTPError(http.StatusConflict, "that os image has been withdrawn; choose another")
	}
	// Signed for the public endpoint, since the host doing the download sits
	// outside this server's network -- the same reason a browser needs that name.
	kernelURL, err := h.blobs.PresignGet(ctx, kernel.ObjectKey, kernel.FileName)
	if err != nil {
		log.Printf("could not presign kernel %s for a create: %v", req.KernelID, err)
		return echo.NewHTTPError(http.StatusBadGateway, "could not prepare the kernel download")
	}
	osImageURL, err := h.blobs.PresignGet(ctx, osImage.ObjectKey, osImage.FileName)
	if err != nil {
		log.Printf("could not presign os image %s for a create: %v", req.OSImageID, err)
		return echo.NewHTTPError(http.StatusBadGateway, "could not prepare the os image download")
	}

	// Boot mode and egress are fixed for self-service creates: direct boot from a
	// kernel plus a tar-built rootfs, with unrestricted outbound. A user chooses
	// the artifacts and the size, not the policy.
	spec := proto.VMSpec{
		Boot:      "direct",
		Kernel:    kernelURL,
		RootfsTar: osImageURL,
		DiskSize:  req.DiskSize,
		CPUs:      int(req.CPUs),
		Memory:    int(req.MemoryMiB),
		EgressAny: true,
	}
	// Written before the job is pushed, same as the admin path: a row with no job
	// is a visible failure, a job with no row is a VM nobody knows about.
	//
	// The name is settled by the insert rather than before it, because uniqueness
	// is the database's answer to give: a generated name that loses the race is
	// redrawn, and only a name the caller chose comes back as a conflict. The spec
	// is built inside the loop so the host names the guest whatever the row ended
	// up holding.
	var row db.Vm
	insert := func(ctx context.Context, q *db.Queries, name string) error {
		spec.Name = name
		raw, err := json.Marshal(spec)
		if err != nil {
			return err
		}
		row, err = q.CreateVM(ctx, db.CreateVMParams{
			ClientID:    pgClientID,
			Name:        name,
			Boot:        spec.Boot,
			CPUs:        req.CPUs,
			MemoryMiB:   req.MemoryMiB,
			DiskMiB:     diskMiB,
			Spec:        raw,
			CreatedBy:   owner,
			DefaultPort: defaultPort,
			PublicPorts: publicPorts,
		})
		if err != nil {
			return err
		}
		// The allowlist rides the same transaction as the row it hangs off. A VM
		// that came up with a partial allowlist would be worse than one that failed
		// outright: it would look created, and be denied things its owner watched
		// themselves ask for.
		for _, nt := range targets {
			if _, _, err := insertTarget(ctx, q, row, nt); err != nil {
				return err
			}
		}
		if ttl == 0 {
			return nil
		}
		// The deadline is written here and nowhere else -- there is no expires_at on
		// vms -- so this insert failing has to take the VM row with it. A sandbox
		// whose expiry was never recorded is one nothing will ever destroy, and
		// nothing would notice either.
		_, err = scheduleTask(ctx, q, scheduleTaskParams{
			Kind:        taskVMExpire,
			SubjectKind: subjectVM,
			SubjectID:   row.ID,
			Payload:     vmExpirePayload{VMName: name, TTLSeconds: req.TTLSeconds},
			Reason:      fmt.Sprintf("temporary sandbox: created with a %s ttl", formatTTL(ttl)),
			CreatedBy:   owner,
			After:       ttl,
			// A host that is down must not turn into a VM that outlives its TTL
			// silently, but it must not exhaust the budget in ten minutes either.
			MaxAttempts: taskExpireAttempts,
		})
		return err
	}
	// One transaction per name attempt rather than one around the loop: a unique
	// violation aborts the transaction it happens in, so a redrawn name needs a
	// fresh one to insert under.
	if err := withVMName(ctx, name, func(ctx context.Context, name string) error {
		// A transaction whenever the create writes more than the one row: the TTL
		// task and the allowlist both have to land with the VM or not at all.
		if ttl == 0 && len(targets) == 0 {
			return insert(ctx, h.q, name)
		}
		return inTx(ctx, h.pool, h.q, func(q *db.Queries) error {
			return insert(ctx, q, name)
		})
	}); err != nil {
		if errors.Is(err, errVMNameTaken) {
			return echo.NewHTTPError(http.StatusConflict, "that name is already taken; choose another")
		}
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
	if err := h.hub.Send(clientID, env); err != nil {
		h.failVM(ctx, row.ID, "could not deliver the job: "+err.Error())
		return echo.NewHTTPError(http.StatusConflict, "could not deliver the job to a host")
	}

	return c.JSON(http.StatusAccepted, toVMDTO(row))
}

// StartVM boots a VM that exists but is not running.
//
// @Summary     Start a VM
// @Description Pushes the job to the host and returns immediately: 202 means the frame was delivered, not that the guest is up. Poll GET /vms/{id} for the outcome.
// @Tags        vms
// @Produce     json
// @Security    BearerAuth
// @Param       id path string true "vm id" format(uuid)
// @Success     202 {object} vmDTO "the job was delivered to the host"
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Failure     409 {object} apiError "the host has no live socket, or has not assigned this VM an id yet"
// @Router      /vms/{id}/start [post]
func (h *UserHandler) StartVM(c *echo.Context) error {
	return h.actOnVM(c, proto.KindVMStart)
}

// StopVM shuts the guest down but leaves its disk on the host, so StartVM can
// boot it again from the same state. The allowance it holds is NOT freed: a
// stopped VM still owns its disk and its slot, and letting a stop free the quota
// would make the limit trivially evadable by stopping and creating in a loop.
//
// @Summary     Stop a VM
// @Description Shuts the guest down but leaves its disk on the host, so a start boots it again from the same state. This does NOT free the quota the VM holds -- only a destroy does. A TTL keeps running while a VM is stopped.
// @Tags        vms
// @Produce     json
// @Security    BearerAuth
// @Param       id path string true "vm id" format(uuid)
// @Success     202 {object} vmDTO "the job was delivered to the host"
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Failure     409 {object} apiError "the host has no live socket, or has not assigned this VM an id yet"
// @Router      /vms/{id}/stop [post]
func (h *UserHandler) StopVM(c *echo.Context) error {
	return h.actOnVM(c, proto.KindVMStop)
}

// DestroyVM stops the guest and deletes its disk on the host. This is the one
// action that frees the allowance the VM is holding.
//
// @Summary     Destroy a VM
// @Description Stops the guest and deletes its disk on the host. This is the one action that frees the quota the VM was holding, and it cannot be undone.
// @Tags        vms
// @Produce     json
// @Security    BearerAuth
// @Param       id path string true "vm id" format(uuid)
// @Success     202 {object} vmDTO "the job was delivered to the host"
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Failure     409 {object} apiError "the host has no live socket, or has not assigned this VM an id yet"
// @Router      /vms/{id}/destroy [post]
func (h *UserHandler) DestroyVM(c *echo.Context) error {
	return h.actOnVM(c, proto.KindVMDestroy)
}

// actOnVM pushes a job naming an existing VM the caller owns. All three actions
// need the same checks -- the row is theirs, the host assigned it an id, the
// client is live, the frame was delivered -- so they share one implementation
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
	// The client treats every action as idempotent, so these guards are about
	// telling the caller its request made no sense rather than about safety.
	switch {
	case kind == proto.KindVMStart && row.Status == "running":
		return echo.NewHTTPError(http.StatusConflict, "this vm is already running")
	case kind == proto.KindVMStop && row.Status == "stopped":
		return echo.NewHTTPError(http.StatusConflict, "this vm is already stopped")
	}

	clientID := uuid.UUID(row.ClientID.Bytes).String()
	if !h.hub.Connected(clientID) {
		return echo.NewHTTPError(http.StatusConflict, "the host running this vm is not connected")
	}
	env, err := proto.NewEnvelope(proto.TypeJob, uuid.UUID(row.ID.Bytes).String(), proto.Job{
		Kind: kind,
		VMID: row.VMID,
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not build the job")
	}
	if err := h.hub.Send(clientID, env); err != nil {
		return echo.NewHTTPError(http.StatusConflict, "could not deliver the job to the host")
	}

	// A destroy makes any pending expiry moot. The task would work that out for
	// itself on its next run -- it checks the VM's status first -- so this is about
	// the audit view: a queue of expiries for VMs that no longer exist is one an
	// operator learns to scroll past.
	if kind == proto.KindVMDestroy {
		cancelTasksForSubject(ctx, h.q, subjectVM, row.ID,
			"the vm was destroyed before its ttl ran out")
	}

	// 202: the row settles when the client reports back, not by the time this
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
	// ExpiresAt is when a temporary allowance is due to be withdrawn, and "" for a
	// permanent one. Read from the pending scheduled task rather than from a column
	// on the row: the deadline is written in one place, and this is a view of it.
	ExpiresAt string `json:"expires_at"`
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

// fillTargetExpiries is fillVMExpiries for allowances: one query for the whole
// list, resolving which of them are temporary and when they end.
func fillTargetExpiries(ctx context.Context, q *db.Queries, items []vmTargetDTO) {
	if len(items) == 0 {
		return
	}
	ids := make([]pgtype.UUID, 0, len(items))
	for _, d := range items {
		if id, err := parseUUID(d.ID); err == nil {
			ids = append(ids, id)
		}
	}
	deadlines := liveTaskDeadlines(ctx, q, subjectVMTarget, ids)
	for i := range items {
		if at, ok := deadlines[items[i].ID]; ok {
			items[i].ExpiresAt = at.Format(time.RFC3339)
		}
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

type updateVMPortsReq struct {
	DefaultPort int32   `json:"default_port"`
	PublicPorts []int32 `json:"public_ports"`
}

// UpdatePorts changes what an owner's VM publishes. Ports are the one part of a
// VM's routing that is safe to change after the fact: the name is a fleet-wide
// identifier that other people's links point at, and the address belongs to the
// host, but which port a request lands on is the owner's business and changes
// whenever they move what they are running.
//
// Takes effect on the host as soon as it is written -- the proxy config is
// regenerated and pushed, the same as a create does.
// UpdatePorts rewrites the routing the host's proxy config is generated from.
//
// @Summary     Set a VM's published ports
// @Description default_port is where a request goes when nothing picks a port; omit it or send 0 for 8000. public_ports is every port the VM publishes -- send an empty list to publish nothing. The list is de-duplicated but not reordered, since its order is the order the generated config lists them in. At most 32 ports.
// @Tags        vms
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       id   path string true "vm id" format(uuid)
// @Param       body body updateVMPortsReq true "ports"
// @Success     200 {object} vmDTO
// @Failure     400 {object} apiError "a port outside 1-65535, or more than 32 of them"
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Router      /vms/{id}/ports [put]
func (h *UserHandler) UpdatePorts(c *echo.Context) error {
	vm, err := h.ownedVM(c)
	if err != nil {
		return err
	}

	var req updateVMPortsReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	defaultPort, publicPorts, err := normalizePorts(req.DefaultPort, req.PublicPorts)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	ctx := c.Request().Context()
	row, err := h.q.UpdateVMPortsForOwner(ctx, db.UpdateVMPortsForOwnerParams{
		ID:          vm.ID,
		CreatedBy:   owner,
		DefaultPort: defaultPort,
		PublicPorts: publicPorts,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "no such vm")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the ports")
	}

	// Whole-host, like every other write that changes routing: the file covers
	// every guest on the machine and is regenerated from the database rather than
	// patched with this row.
	pushProxyConfig(ctx, h.q, h.hub, h.proxy, vm.ClientID)
	// Carries the urls like GetVM does: the detail page shows the saved row
	// straight back, so leaving them out would make the links vanish on save.
	d := toVMDTO(row)
	d.URL = h.vmURL(ctx, row)
	d.ConsoleURL = h.consoleURL(ctx, row)
	return c.JSON(http.StatusOK, d)
}

// ListTargets returns one VM's egress allowlist.
//
// @Summary     List a VM's allowed destinations
// @Description Everything this guest is permitted to reach. expires_at is set on temporary allowances and empty on permanent ones.
// @Tags        egress
// @Produce     json
// @Security    BearerAuth
// @Param       id path string true "vm id" format(uuid)
// @Success     200 {object} targetList
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Router      /vms/{id}/targets [get]
func (h *UserHandler) ListTargets(c *echo.Context) error {
	vm, err := h.ownedVM(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	rows, err := h.q.ListVMNetworkTargets(ctx, vm.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not list destinations")
	}
	items := make([]vmTargetDTO, 0, len(rows))
	for _, t := range rows {
		items = append(items, toVMTargetDTO(t))
	}
	fillTargetExpiries(ctx, h.q, items)
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

type createTargetReq struct {
	// Kind is what the caller says this is. Optional: omitted, the destination is
	// classified for them. Supplied and disagreeing with the destination, the
	// request is rejected -- a form that asked for an address and got a hostname
	// has a mistake in it, and quietly storing the other kind hides it.
	Kind        string `json:"kind"`
	Destination string `json:"destination"`
	// Transport is ignored when the destination is a domain: both rules a domain
	// compiles to are tcp by construction, so there is nothing to choose.
	Transport string `json:"transport"`
	// Ports means different things to the two kinds. For an address it is
	// Suricata's port syntax and goes straight into a rule header. For a domain it
	// is which of the two web ports the name is allowed on -- see domainPorts in
	// suricata_rules.go, and domainTargetPorts below for what is accepted.
	Ports string `json:"ports"`
	Note  string `json:"note"`

	// TTLSeconds makes this a temporary allowance: the control plane removes it
	// this many seconds from now and regenerates the host's policy. 0 is a
	// permanent one, which is what an omitted field means.
	TTLSeconds int64 `json:"ttl_seconds"`
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

// domainTargetPorts normalises the ports field of a domain allowance to one of
// the four values the schema's shape constraint accepts, or explains why it
// cannot.
//
// The choice is narrow on purpose, and the reason is not the rule generator: the
// ports Suricata looks for http and tls on come from suricata.yaml, which is
// compiled per host from its VM pool and pushed when the client connects. A domain
// allowed on 8443 would compile to a rule that parses, loads, and never matches
// anything -- so it is rejected here rather than granted in name only.
func domainTargetPorts(ports string) (string, error) {
	switch ports {
	case "", "80,443", "443,80":
		// Both web ports, which is also what every row written before this field
		// existed means.
		return "", nil
	case "80", "443", domainPortsNone:
		return ports, nil
	}
	return "", errors.New(
		"a domain may be allowed on 443, on 80, on both, or on neither ('none', which lets the name resolve without granting access); " +
			"for any other port allow the address instead")
}

// normalizedTarget is one requested destination after validation: the columns it
// becomes, and how long it lives.
type normalizedTarget struct {
	params db.CreateVMNetworkTargetParams
	ttl    time.Duration
	// ttlSeconds is what was asked for, carried through to the expiry task's
	// payload -- that records the request, not the deadline computed from it.
	ttlSeconds int64
}

// normalizeTarget settles one requested destination into the row it becomes, or
// explains why it cannot be one.
//
// Split out of CreateTarget so that naming destinations while creating a VM
// applies exactly these rules. Two copies of this would drift, and the half that
// drifted would be the one handing out access nobody checked.
//
// VMID is left unset: the create path does not know it until the row the
// allowance hangs off has been written.
func normalizeTarget(req createTargetReq) (normalizedTarget, error) {
	req.Destination = strings.TrimSpace(req.Destination)
	req.Ports = strings.ReplaceAll(strings.TrimSpace(req.Ports), " ", "")
	req.Note = strings.TrimSpace(req.Note)
	req.Transport = strings.ToLower(strings.TrimSpace(req.Transport))

	ttl, err := parseTTLSeconds(req.TTLSeconds)
	if err != nil {
		return normalizedTarget{}, err
	}

	if req.Destination == "" {
		return normalizedTarget{}, errors.New("a destination is required")
	}
	kind, err := classifyDestination(req.Destination)
	if err != nil {
		return normalizedTarget{}, err
	}
	if declared := strings.ToLower(strings.TrimSpace(req.Kind)); declared != "" && declared != kind {
		switch declared {
		case "domain":
			return normalizedTarget{}, errors.New("that is an address, not a domain")
		case "ip":
			return normalizedTarget{}, errors.New("that is a domain, not an IP address or CIDR")
		default:
			return normalizedTarget{}, errors.New("type must be domain or ip")
		}
	}

	// The kind decides which of the remaining fields mean anything. Transport is
	// cleared rather than rejected for a domain: the form hides it, so a stale
	// value arriving is this server's problem to normalise, not the caller's to
	// fix. Ports is not cleared -- for a domain it carries which web ports the
	// name is allowed on, so a wrong value there is a real disagreement about what
	// is being granted and is reported instead.
	if kind == "domain" {
		req.Transport = ""
		ports, err := domainTargetPorts(req.Ports)
		if err != nil {
			return normalizedTarget{}, err
		}
		req.Ports = ports
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
		case "icmp":
			// Cleared rather than rejected: icmp has no ports, the form hides the field
			// when it is chosen, and a value arriving anyway is a stale form rather than
			// something the user is asking for.
			req.Ports = ""
		default:
			return normalizedTarget{}, errors.New("transport must be tcp, udp, icmp or any")
		}
		if req.Ports != "" {
			if !portsPattern.MatchString(req.Ports) {
				return normalizedTarget{}, errors.New(
					"ports must be a port, a list like 80,443, or a range like 1000:2000")
			}
			for _, part := range strings.Split(strings.ReplaceAll(req.Ports, ":", ","), ",") {
				n, err := strconv.Atoi(part)
				if err != nil || n < 1 || n > 65535 {
					return normalizedTarget{}, errors.New("ports must be between 1 and 65535")
				}
			}
		}
	}

	return normalizedTarget{
		params: db.CreateVMNetworkTargetParams{
			Destination: req.Destination,
			Kind:        kind,
			Transport:   req.Transport,
			Ports:       req.Ports,
			Note:        req.Note,
		},
		ttl:        ttl,
		ttlSeconds: req.TTLSeconds,
	}, nil
}

// insertTarget writes one allowance and, when it is temporary, the task that
// withdraws it -- inside whatever transaction the caller is running.
//
// Shared by the add-a-destination route and the create-a-VM path so that a
// temporary allowance is recorded identically by both. The destination comes off
// the normalized params rather than the request: for a domain those differ, and
// the audit record has to name the row that was actually written.
func insertTarget(ctx context.Context, q *db.Queries, vm db.Vm, nt normalizedTarget) (db.VmNetworkTarget, db.ScheduledTask, error) {
	params := nt.params
	params.VMID = vm.ID
	t, err := q.CreateVMNetworkTarget(ctx, params)
	if err != nil || nt.ttl == 0 {
		return t, db.ScheduledTask{}, err
	}
	expiry, err := scheduleTask(ctx, q, scheduleTaskParams{
		Kind:        taskVMTargetExpire,
		SubjectKind: subjectVMTarget,
		SubjectID:   t.ID,
		Payload: vmTargetExpirePayload{
			VMID:        uuid.UUID(vm.ID.Bytes).String(),
			VMName:      vm.Name,
			Destination: params.Destination,
			TTLSeconds:  nt.ttlSeconds,
		},
		Reason:    fmt.Sprintf("temporary access to %s for %s", params.Destination, formatTTL(nt.ttl)),
		CreatedBy: vm.CreatedBy,
		After:     nt.ttl,
		// Removing an allowance only needs the database; the push that follows is
		// best-effort and self-heals when the host reconnects. So the default budget
		// is plenty -- unlike a VM expiry, this does not wait on a machine.
	})
	return t, expiry, err
}

// CreateTarget adds one destination to a VM's egress allowlist.
//
// @Summary     Allow a destination
// @Description destination is a domain, an IP address, or a CIDR. Omit kind to have it classified for you; supply it and it must agree, since a form that asked for an address and got a hostname has a mistake in it.
// @Description
// @Description For an address, ports is Suricata's syntax (`22`, `80,443`, `8000:8100`) and transport is tcp, udp or any. For a domain, ports may only be `443`, `80`, both, or `none` -- the ports Suricata looks for http and tls on are fixed per host, so a domain allowed on 8443 would compile to a rule that never matches. Allow the address instead.
// @Description
// @Description Only tls and http carry the destination name in the traffic, so ssh or postgres to a hostname is not expressible: use /vms/{id}/targets/resolve and record the addresses.
// @Description
// @Description Set ttl_seconds for a temporary allowance the control plane withdraws on its own.
// @Tags        egress
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       id   path string true "vm id" format(uuid)
// @Param       body body createTargetReq true "destination is required"
// @Success     201 {object} vmTargetDTO
// @Failure     400 {object} apiError "a destination that is neither a name nor an address, or a port the rules cannot express"
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Failure     409 {object} apiError "that destination is already on the list"
// @Router      /vms/{id}/targets [post]
func (h *UserHandler) CreateTarget(c *echo.Context) error {
	vm, err := h.ownedVM(c)
	if err != nil {
		return err
	}

	var req createTargetReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	nt, err := normalizeTarget(req)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	ttl := nt.ttl

	ctx := c.Request().Context()
	// With a TTL the row and its expiry are written together, for the same reason
	// as a VM's: the deadline exists only as a task, so an allowance recorded
	// without one is a temporary grant that turns out to be permanent -- the exact
	// failure this feature is meant to prevent.
	var t db.VmNetworkTarget
	var expiry db.ScheduledTask
	create := func(q *db.Queries) error {
		var err error
		t, expiry, err = insertTarget(ctx, q, vm, nt)
		return err
	}
	if ttl > 0 {
		err = inTx(ctx, h.pool, h.q, create)
	} else {
		err = create(h.q)
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return echo.NewHTTPError(http.StatusConflict, "that destination is already on the list")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not add the destination")
	}
	// Whole-host, not whole-VM: both files cover every guest on the host, so they
	// are regenerated from the database rather than patched with this row. The
	// Corefile is what lets the guest resolve the name at all, so a ruleset sent
	// without it is an allowance that cannot be used.
	pushSuricataRules(ctx, h.q, h.hub, vm.ClientID)
	pushCoreDNSConfig(ctx, h.q, h.hub, vm.ClientID)

	dto := toVMTargetDTO(t)
	if expiry.RunAt.Valid {
		// The deadline the database computed, not now()+ttl worked out here: those
		// differ by however far this process's clock is off, and the row is the one
		// that decides when the allowance ends.
		dto.ExpiresAt = expiry.RunAt.Time.Format(time.RFC3339)
	}
	return c.JSON(http.StatusCreated, dto)
}

// Resolving a name for the user is a convenience with a sharp edge, so both are
// bounded here.
const (
	// resolveTimeout is short: this is a form waiting on it, and a name that takes
	// longer than this to answer is one the guest would have trouble with too.
	resolveTimeout = 3 * time.Second

	// resolveMaxAddresses caps what one name can turn into. A CDN answers with a
	// handful of addresses out of a pool of thousands, and recording twenty of them
	// is neither an allowlist nor an explanation -- it is a suggestion that the user
	// has allowed something they have not.
	resolveMaxAddresses = 8
)

type resolveHostReq struct {
	Host string `json:"host"`
}

// ResolveTargetHost answers what a hostname currently resolves to, so the UI can
// offer to record those addresses as allowances.
//
// This exists because of a limit that cannot be designed away: only tls and http
// carry the destination name in the traffic suricata sees, so an allowance for ssh
// or postgres to a hostname is not expressible -- there is nothing in the packets
// to check a name against. Such access has to be granted by address, and a user
// who thinks in names needs help turning one into the other.
//
// Deliberately not a background job that keeps the two in step. Addresses move,
// and something re-resolving on a timer would silently widen an allowlist nobody
// re-read. This returns what it found, the caller records it, and what the page
// lists afterwards is exactly what is enforced.
//
// @Summary     Resolve a hostname
// @Description What a name currently resolves to, so you can record those addresses as allowances. IPv4 only -- every rule and nftables element downstream is IPv4, so an AAAA record would be an address nothing can express. At most 8 addresses; truncated says when there were more.
// @Description
// @Description A name that does not resolve is a 200 with error set, not a failure: that is an answer about the name rather than about this server. Deliberately a one-shot lookup and not a subscription -- addresses move, and something re-resolving on a timer would silently widen an allowlist nobody re-read.
// @Description
// @Description Scoped to a VM you own even though the answer is not VM-specific, so this is not a public name-resolution service.
// @Tags        egress
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       id   path string true "vm id" format(uuid)
// @Param       body body resolveHostReq true "host"
// @Success     200 {object} resolvedHost
// @Failure     400 {object} apiError "that is not a hostname"
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Router      /vms/{id}/targets/resolve [post]
func (h *UserHandler) ResolveTargetHost(c *echo.Context) error {
	// Scoped to a VM the caller owns even though the answer is not VM-specific: it
	// is a lookup this server makes on request, and an unauthenticated one would be
	// a name-resolution service for anything that can reach the API.
	if _, err := h.ownedVM(c); err != nil {
		return err
	}

	var req resolveHostReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	host := strings.ToLower(strings.TrimSpace(req.Host))
	if host == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "a hostname is required")
	}
	// The same pattern CreateTarget classifies with, so a name that resolves here is
	// one that can be recorded there -- and it is what keeps this from being handed
	// anything but a hostname.
	if !hostPattern.MatchString(host) {
		return echo.NewHTTPError(http.StatusBadRequest, "that is not a hostname")
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), resolveTimeout)
	defer cancel()

	addrs, err := h.resolver(ctx).LookupIP(ctx, "ip4", host)
	if err != nil {
		// Not a 500: a name that does not resolve is an answer about the name, not a
		// failure of this server, and the form needs to say so rather than break.
		return c.JSON(http.StatusOK, map[string]any{
			"host": host, "addresses": []string{}, "error": "that name did not resolve",
		})
	}

	// IPv4 only, and not an oversight: every rule header and every nftables element
	// in this system is IPv4, so an AAAA record would be an address recorded here
	// that nothing downstream can express.
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if v4 := a.To4(); v4 != nil {
			out = append(out, v4.String())
		}
	}
	sort.Strings(out)
	truncated := false
	if len(out) > resolveMaxAddresses {
		out, truncated = out[:resolveMaxAddresses], true
	}
	return c.JSON(http.StatusOK, map[string]any{
		"host": host, "addresses": out, "truncated": truncated,
	})
}

// resolver dials the same upstream the hosts' resolvers forward to, so what this
// tells a user matches what their guest will be told. Falling back to the system
// resolver when the setting is unreadable would answer from somewhere else
// entirely, which for a split-horizon name is a different set of addresses.
func (h *UserHandler) resolver(ctx context.Context) *net.Resolver {
	upstream := setting(ctx, h.q, settingResolverUpstream)
	if upstream == "" {
		return net.DefaultResolver
	}
	if _, _, err := net.SplitHostPort(upstream); err != nil {
		upstream = net.JoinHostPort(upstream, "53")
	}
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, upstream)
		},
	}
}

// DeleteTarget withdraws one destination from a VM's egress allowlist.
//
// @Summary     Remove an allowed destination
// @Description Cancels any pending expiry on it and pushes the new policy to the host. Until that push lands the guest still has the access, so treat the 204 as "recorded", not "already enforced".
// @Tags        egress
// @Produce     json
// @Security    BearerAuth
// @Param       id        path string true "vm id" format(uuid)
// @Param       target_id path string true "destination id" format(uuid)
// @Success     204 "removed"
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Router      /vms/{id}/targets/{target_id} [delete]
func (h *UserHandler) DeleteTarget(c *echo.Context) error {
	vm, err := h.ownedVM(c)
	if err != nil {
		return err
	}
	targetID, err := parseUUID(c.Param("target_id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid destination id")
	}
	ctx := c.Request().Context()
	if err := h.q.DeleteVMNetworkTarget(ctx, db.DeleteVMNetworkTargetParams{
		ID: targetID, VMID: vm.ID,
	}); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not remove the destination")
	}
	// The row is gone, so its expiry has nothing to expire. Withdrawn here rather
	// than left for the task to discover, so the pending queue stays a list of
	// things that are actually going to happen.
	cancelTasksForSubject(ctx, h.q, subjectVMTarget, targetID,
		"the destination was removed before its ttl ran out")
	// A removal has to reach the host even more urgently than an addition: until
	// it does, the guest still has the access the user just revoked. The Corefile
	// withdraws the name and the ruleset withdraws the access; whichever arrives
	// second is the one that finishes the revocation.
	pushSuricataRules(ctx, h.q, h.hub, vm.ClientID)
	pushCoreDNSConfig(ctx, h.q, h.hub, vm.ClientID)
	return c.NoContent(http.StatusNoContent)
}

func (h *UserHandler) failVM(ctx context.Context, id pgtype.UUID, msg string) {
	if err := h.q.MarkVMFailed(ctx, db.MarkVMFailedParams{ID: id, LastError: msg}); err != nil {
		log.Printf("could not mark vm failed: %v", err)
	}
}
