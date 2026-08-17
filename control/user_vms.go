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
	d := toVMDTO(v)
	d.URL = h.vmURL(c.Request().Context(), v)
	d.ConsoleURL = h.consoleURL(c.Request().Context(), v)
	return c.JSON(http.StatusOK, d)
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

// ListHosts offers only clients with a live socket. An client whose row still
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
}

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
	if err := withVMName(ctx, name, func(ctx context.Context, name string) error {
		spec.Name = name
		raw, err := json.Marshal(spec)
		if err != nil {
			return err
		}
		row, err = h.q.CreateVM(ctx, db.CreateVMParams{
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
		return err
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
	// Transport is ignored when the destination is a domain: both rules a domain
	// compiles to are tcp by construction, so there is nothing to choose.
	Transport string `json:"transport"`
	// Ports means different things to the two kinds. For an address it is
	// Suricata's port syntax and goes straight into a rule header. For a domain it
	// is which of the two web ports the name is allowed on -- see domainPorts in
	// suricata_rules.go, and domainTargetPorts below for what is accepted.
	Ports string `json:"ports"`
	Note  string `json:"note"`
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

	// The kind decides which of the remaining fields mean anything. Transport is
	// cleared rather than rejected for a domain: the form hides it, so a stale
	// value arriving is this server's problem to normalise, not the caller's to
	// fix. Ports is not cleared -- for a domain it now carries which web ports the
	// name is allowed on, so a wrong value there is a real disagreement about what
	// is being granted and is reported instead.
	if kind == "domain" {
		req.Transport = ""
		ports, err := domainTargetPorts(req.Ports)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
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
			return echo.NewHTTPError(http.StatusBadRequest, "transport must be tcp, udp, icmp or any")
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
	// Whole-host, not whole-VM: both files cover every guest on the host, so they
	// are regenerated from the database rather than patched with this row. The
	// Corefile is what lets the guest resolve the name at all, so a ruleset sent
	// without it is an allowance that cannot be used.
	pushSuricataRules(c.Request().Context(), h.q, h.hub, vm.ClientID)
	pushCoreDNSConfig(c.Request().Context(), h.q, h.hub, vm.ClientID)
	return c.JSON(http.StatusCreated, toVMTargetDTO(t))
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
	// it does, the guest still has the access the user just revoked. The Corefile
	// withdraws the name and the ruleset withdraws the access; whichever arrives
	// second is the one that finishes the revocation.
	pushSuricataRules(c.Request().Context(), h.q, h.hub, vm.ClientID)
	pushCoreDNSConfig(c.Request().Context(), h.q, h.hub, vm.ClientID)
	return c.NoContent(http.StatusNoContent)
}

func (h *UserHandler) failVM(ctx context.Context, id pgtype.UUID, msg string) {
	if err := h.q.MarkVMFailed(ctx, db.MarkVMFailedParams{ID: id, LastError: msg}); err != nil {
		log.Printf("could not mark vm failed: %v", err)
	}
}
