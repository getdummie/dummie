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

type UserHandler struct {
	q *db.Queries
	pool *pgxpool.Pool
	hub  *Hub
	prod bool
	proxy proxyAuthConfig
	blobs *blobStore
	ch driver.Conn
}

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

func (h *UserHandler) vmDomainTLD(ctx context.Context, v db.Vm) string {
	if v.Name == "" {
		return ""
	}
	client, err := h.q.GetClientByID(ctx, v.ClientID)
	if err != nil || !client.DomainID.Valid {
		return ""
	}
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

func callerID(c *echo.Context) (pgtype.UUID, error) {
	uid, _ := c.Get("uid").(string)
	if uid == "" {
		return pgtype.UUID{}, errors.New("no caller id on the request")
	}
	return parseUUID(uid)
}

// @Summary     List your VMs
// @Description Scoped to you by the query itself. url, console_url and desktop_url are empty here: resolving them would be a query per row, so ask for a single VM when you need them.
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
			return echo.NewHTTPError(http.StatusNotFound, "no such vm")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read vm")
	}
	ctx := c.Request().Context()
	items := []vmDTO{toVMDTO(v)}
	items[0].URL = h.vmURL(ctx, v)
	items[0].ConsoleURL = h.consoleURL(ctx, v)
	items[0].DesktopURL = h.desktopURL(ctx, v)
	fillVMExpiries(ctx, h.q, items)
	return c.JSON(http.StatusOK, items[0])
}

type quotaDTO struct {
	VCPULimit      int32 `json:"vcpu_limit"`
	MemoryLimitMiB int32 `json:"memory_limit_mib"`
	DiskLimitMiB   int32 `json:"disk_limit_mib"`
	VCPUUsed       int32 `json:"vcpu_used"`
	MemoryUsedMiB  int32 `json:"memory_used_mib"`
	DiskUsedMiB    int32 `json:"disk_used_mib"`
}

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

type hostDTO struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
}

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

type userArtifactDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	SizeBytes   int64  `json:"size_bytes"`
	CreatedAt   string `json:"created_at"`
}

const maxArtifactChoices = 100

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

type createVMReq struct {
	ClientID  string `json:"client_id"`
	Name      string `json:"name"`
	CPUs      int32  `json:"cpus"`
	MemoryMiB int32  `json:"memory_mib"`
	DiskSize string `json:"disk_size"`
	KernelID string `json:"kernel_id"`
	OSImageID string `json:"osimage_id"`

	DefaultPort int32 `json:"default_port"`
	PublicPorts []int32 `json:"public_ports"`

	TTLSeconds int64 `json:"ttl_seconds"`

	Targets []createTargetReq `json:"targets"`
}

const maxCreateTargets = 32

const defaultVMPort = 8000

const maxPublicPorts = 32

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

var sizePattern = regexp.MustCompile(`^[0-9]+[KkMmGgTt]?$`)

var sha256Pattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

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
	name, err := validateVMName(req.Name)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if err := vmNameAllowed(c.Request().Context(), h.q, name); err != nil {
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

	if len(req.Targets) > maxCreateTargets {
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf(
			"a vm can be created with at most %d destinations; add the rest afterwards", maxCreateTargets))
	}
	targets := make([]normalizedTarget, 0, len(req.Targets))
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

	u, err := h.q.GetUserByID(ctx, owner)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read your account")
	}

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

	spec := proto.VMSpec{
		Boot:      "direct",
		Kernel:    kernelURL,
		RootfsTar: osImageURL,
		DiskSize:  req.DiskSize,
		CPUs:      int(req.CPUs),
		Memory:    int(req.MemoryMiB),
		EgressAny: true,
	}
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
		for _, nt := range targets {
			if _, _, err := insertTarget(ctx, q, row, nt); err != nil {
				return err
			}
		}
		if ttl == 0 {
			return nil
		}
		_, err = scheduleTask(ctx, q, scheduleTaskParams{
			Kind:        taskVMExpire,
			SubjectKind: subjectVM,
			SubjectID:   row.ID,
			Payload:     vmExpirePayload{VMName: name, TTLSeconds: req.TTLSeconds},
			Reason:      fmt.Sprintf("temporary sandbox: created with a %s ttl", formatTTL(ttl)),
			CreatedBy:   owner,
			After:       ttl,
			MaxAttempts: taskExpireAttempts,
		})
		return err
	}
	if err := withVMName(ctx, name, func(ctx context.Context, name string) error {
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

	if kind == proto.KindVMDestroy {
		cancelTasksForSubject(ctx, h.q, subjectVM, row.ID,
			"the vm was destroyed before its ttl ran out")
	}

	return c.JSON(http.StatusAccepted, toVMDTO(row))
}

// @Summary     Delete a VM
// @Description Destroys the guest on its host and deletes the record. Unlike /destroy, nothing is left behind to look up afterwards. 202 means the destroy was delivered and the record will go when the host confirms; 204 means there was nothing on any host and the record is already gone. Frees the quota the VM was holding.
// @Tags        vms
// @Produce     json
// @Security    BearerAuth
// @Param       id path string true "vm id" format(uuid)
// @Success     202 {object} vmDTO "the destroy was delivered to the host; the record goes when the host confirms"
// @Success     204 "there was nothing on a host; the record is deleted"
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Failure     409 {object} apiError "the create is still in flight, or the host has no live socket"
// @Router      /vms/{id} [delete]
func (h *UserHandler) DeleteVM(c *echo.Context) error {
	row, err := h.ownedVM(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()

	if row.Status == "pending" {
		return echo.NewHTTPError(http.StatusConflict, "this vm is still being created; try again once it has settled")
	}

	// The row itself goes with the VM, but the certificate it holds in object
	// storage would outlive it.
	if cd, err := h.q.GetCustomDomainByVM(ctx, row.ID); err == nil {
		if err := h.dropCustomDomain(ctx, cd, row.ClientID); err != nil {
			log.Printf("could not drop the custom domain of vm %s: %v", row.Name, err)
		}
	}

	if row.VMID == "" || row.Status == "failed" || row.Status == "gone" {
		if err := h.q.DeleteVM(ctx, row.ID); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "could not delete vm")
		}
		cancelTasksForSubject(ctx, h.q, subjectVM, row.ID, "the vm was deleted")
		return c.NoContent(http.StatusNoContent)
	}

	clientID := uuid.UUID(row.ClientID.Bytes).String()
	if !h.hub.Connected(clientID) {
		return echo.NewHTTPError(http.StatusConflict, "the host running this vm is not connected")
	}
	rowID := uuid.UUID(row.ID.Bytes).String()
	env, err := proto.NewEnvelope(proto.TypeJob, rowID, proto.Job{
		Kind: proto.KindVMDestroy,
		VMID: row.VMID,
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not build the job")
	}
	h.hub.MarkPurge(rowID)
	if err := h.hub.Send(clientID, env); err != nil {
		h.hub.TakePurge(rowID)
		return echo.NewHTTPError(http.StatusConflict, "could not deliver the job to the host")
	}
	cancelTasksForSubject(ctx, h.q, subjectVM, row.ID, "the vm was deleted before its ttl ran out")

	return c.JSON(http.StatusAccepted, toVMDTO(row))
}

type vmTargetDTO struct {
	ID          string `json:"id"`
	Destination string `json:"destination"`
	Kind        string `json:"kind"`
	Transport   string `json:"transport"`
	Ports       string `json:"ports"`
	Note        string `json:"note"`
	CreatedAt   string `json:"created_at"`
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

	pushProxyConfig(ctx, h.q, h.hub, h.proxy, vm.ClientID)
	d := toVMDTO(row)
	d.URL = h.vmURL(ctx, row)
	d.ConsoleURL = h.consoleURL(ctx, row)
	d.DesktopURL = h.desktopURL(ctx, row)
	return c.JSON(http.StatusOK, d)
}

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
	Kind        string `json:"kind"`
	Destination string `json:"destination"`
	Transport string `json:"transport"`
	Ports string `json:"ports"`
	Note  string `json:"note"`

	TTLSeconds int64 `json:"ttl_seconds"`
}

var portsPattern = regexp.MustCompile(`^[0-9]+(:[0-9]+)?(,[0-9]+(:[0-9]+)?)*$`)

var hostPattern = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)+$`)

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

func domainTargetPorts(ports string) (string, error) {
	switch ports {
	case "", "80,443", "443,80":
		return "", nil
	case "80", "443", domainPortsNone:
		return ports, nil
	}
	return "", errors.New(
		"a domain may be allowed on 443, on 80, on both, or on neither ('none', which lets the name resolve without granting access); " +
			"for any other port allow the address instead")
}

type normalizedTarget struct {
	params db.CreateVMNetworkTargetParams
	ttl    time.Duration
	ttlSeconds int64
}

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

	if kind == "domain" {
		req.Transport = ""
		ports, err := domainTargetPorts(req.Ports)
		if err != nil {
			return normalizedTarget{}, err
		}
		req.Ports = ports
		req.Destination = strings.ToLower(req.Destination)
	} else {
		if req.Transport == "" {
			req.Transport = "tcp"
		}
		switch req.Transport {
		case "tcp", "udp", "any":
		case "icmp":
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
	})
	return t, expiry, err
}

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
	pushSuricataRules(ctx, h.q, h.hub, vm.ClientID)
	pushCoreDNSConfig(ctx, h.q, h.hub, vm.ClientID)

	dto := toVMTargetDTO(t)
	if expiry.RunAt.Valid {
		dto.ExpiresAt = expiry.RunAt.Time.Format(time.RFC3339)
	}
	return c.JSON(http.StatusCreated, dto)
}

// @Summary     Edit an allowed destination
// @Description Replaces the entry with the one in the body, validated exactly as a create is. Any pending expiry on the old entry is cancelled and ttl_seconds starts a fresh one, so an edit that leaves the ttl alone still restarts the clock.
// @Description
// @Description The entry is rewritten rather than patched, so the id in the response is a new one.
// @Tags        egress
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       id        path string true "vm id" format(uuid)
// @Param       target_id path string true "destination id" format(uuid)
// @Param       body body createTargetReq true "destination is required"
// @Success     200 {object} vmTargetDTO
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Failure     409 {object} apiError "another entry already covers that destination"
// @Router      /vms/{id}/targets/{target_id} [put]
func (h *UserHandler) UpdateTarget(c *echo.Context) error {
	vm, err := h.ownedVM(c)
	if err != nil {
		return err
	}
	targetID, err := parseUUID(c.Param("target_id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid destination id")
	}

	var req createTargetReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	nt, err := normalizeTarget(req)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	ctx := c.Request().Context()
	existing, err := h.q.GetVMNetworkTargetForExpiry(ctx, targetID)
	if err != nil || existing.VMID != vm.ID {
		return echo.NewHTTPError(http.StatusNotFound, "no such destination")
	}

	var t db.VmNetworkTarget
	var expiry db.ScheduledTask
	err = inTx(ctx, h.pool, h.q, func(q *db.Queries) error {
		if err := q.DeleteVMNetworkTarget(ctx, db.DeleteVMNetworkTargetParams{
			ID: targetID, VMID: vm.ID,
		}); err != nil {
			return err
		}
		var err error
		t, expiry, err = insertTarget(ctx, q, vm, nt)
		return err
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return echo.NewHTTPError(http.StatusConflict, "that destination is already on the list")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save the destination")
	}
	cancelTasksForSubject(ctx, h.q, subjectVMTarget, targetID,
		"the destination was edited")
	pushSuricataRules(ctx, h.q, h.hub, vm.ClientID)
	pushCoreDNSConfig(ctx, h.q, h.hub, vm.ClientID)

	dto := toVMTargetDTO(t)
	if expiry.RunAt.Valid {
		dto.ExpiresAt = expiry.RunAt.Time.Format(time.RFC3339)
	}
	return c.JSON(http.StatusOK, dto)
}

const (
	resolveTimeout = 3 * time.Second

	resolveMaxAddresses = 8
)

type resolveHostReq struct {
	Host string `json:"host"`
}

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
	if !hostPattern.MatchString(host) {
		return echo.NewHTTPError(http.StatusBadRequest, "that is not a hostname")
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), resolveTimeout)
	defer cancel()

	addrs, err := h.resolver(ctx).LookupIP(ctx, "ip4", host)
	if err != nil {
		return c.JSON(http.StatusOK, map[string]any{
			"host": host, "addresses": []string{}, "error": "that name did not resolve",
		})
	}

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

func (h *UserHandler) resolver(ctx context.Context) *net.Resolver {
	return configuredResolver(ctx, h.q)
}

func configuredResolver(ctx context.Context, q *db.Queries) *net.Resolver {
	upstream := setting(ctx, q, settingResolverUpstream)
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
	cancelTasksForSubject(ctx, h.q, subjectVMTarget, targetID,
		"the destination was removed before its ttl ran out")
	pushSuricataRules(ctx, h.q, h.hub, vm.ClientID)
	pushCoreDNSConfig(ctx, h.q, h.hub, vm.ClientID)
	return c.NoContent(http.StatusNoContent)
}

func (h *UserHandler) failVM(ctx context.Context, id pgtype.UUID, msg string) {
	if err := h.q.MarkVMFailed(ctx, db.MarkVMFailedParams{ID: id, LastError: msg}); err != nil {
		log.Printf("could not mark vm failed: %v", err)
	}
}
