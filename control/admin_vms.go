package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
// client downloads images and builds a filesystem, which takes minutes. So the
// endpoint records the intent, pushes the job, and answers 202 with the row.
// The row's status is the whole answer to "did it work" -- polling it is the
// intended use, not a workaround for a missing synchronous API.

type vmDTO struct {
	ID        string `json:"id"`
	ClientID  string `json:"client_id"`
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
	// client's inventory report, or created before ownership was recorded.
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

	// ExpiresAt is when a TTL'd VM is due to be destroyed, and "" for one with no
	// TTL. It comes from the pending scheduled task, which is the only place the
	// deadline is written -- there is no expires_at column, so that the answer to
	// "when does this die" cannot be two different things.
	//
	// A deadline in the past means the runner has not got to it yet. That is worth
	// showing as-is rather than hiding: the VM really is still there.
	ExpiresAt string `json:"expires_at"`

	// ReportedAt is when the host last confirmed this VM; "" means it never has.
	// 'running' is the host's claim as of that moment, not a live observation, so
	// a reader has to weigh the status against this timestamp -- a status of
	// 'running' with a stale ReportedAt means "was running when last seen".
	ReportedAt string `json:"reported_at"`
}

func toVMDTO(v db.Vm) vmDTO {
	d := vmDTO{
		ID:          uuid.UUID(v.ID.Bytes).String(),
		ClientID:    uuid.UUID(v.ClientID.Bytes).String(),
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

// fillVMExpiries adds the TTL deadline to a page of VM DTOs. One query for the
// whole page, which is the reason the lookup is batched at all: resolving it per
// row would be a query per VM on every render of a list.
//
// Called by every route that returns VMs. A route that forgot would show a
// temporary sandbox as though it were permanent, which is worse than showing no
// deadline at all -- so it goes next to toVMDTO where it is hard to miss.
func fillVMExpiries(ctx context.Context, q *db.Queries, items []vmDTO) {
	if len(items) == 0 {
		return
	}
	ids := make([]pgtype.UUID, 0, len(items))
	for _, d := range items {
		if id, err := parseUUID(d.ID); err == nil {
			ids = append(ids, id)
		}
	}
	deadlines := liveTaskDeadlines(ctx, q, subjectVM, ids)
	for i := range items {
		if at, ok := deadlines[items[i].ID]; ok {
			items[i].ExpiresAt = at.Format(time.RFC3339)
		}
	}
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
	fillVMExpiries(ctx, h.q, items)
	return c.JSON(http.StatusOK, pageEnvelope(items, total, limit, offset))
}

func (h *AdminHandler) ListClientVMs(c *echo.Context) error {
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid client id")
	}
	ctx := c.Request().Context()
	limit, offset := pageParams(c)
	total, err := h.q.CountVMsByClient(ctx, pgID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not count vms")
	}
	rows, err := h.q.ListVMsByClient(ctx, db.ListVMsByClientParams{
		ClientID: pgID, Limit: limit, Offset: offset,
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

func (h *AdminHandler) GetVM(c *echo.Context) error {
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid vm id")
	}
	ctx := c.Request().Context()
	v, err := h.q.GetVM(ctx, pgID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "no such vm")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read vm")
	}
	items := []vmDTO{toVMDTO(v)}
	fillVMExpiries(ctx, h.q, items)
	return c.JSON(http.StatusOK, items[0])
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
	fillTargetExpiries(ctx, h.q, items)
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// adminCreateVMReq is the client's own VM spec, inline, plus the routing the
// control plane owns. Embedded rather than nested so the body stays the spec
// `dclient vm create` accepts, with two more fields on it.
type adminCreateVMReq struct {
	proto.VMSpec
	DefaultPort int32   `json:"default_port"`
	PublicPorts []int32 `json:"public_ports"`
	// TTLSeconds destroys the VM this many seconds after the row is written. Same
	// meaning as on the self-service create, and measured the same way -- from the
	// create, running while the VM is stopped.
	TTLSeconds int64 `json:"ttl_seconds"`
}

// CreateVM asks an client to spin up a VM. The body is the client's own VM spec,
// so anything `dclient vm create` accepts is accepted here too.
func (h *AdminHandler) CreateVM(c *echo.Context) error {
	clientID := c.Param("id")
	pgClientID, err := parseUUID(clientID)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid client id")
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
	ttl, err := parseTTLSeconds(req.TTLSeconds)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	ctx := c.Request().Context()
	client, err := h.q.GetClientByID(ctx, pgClientID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "no such client")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read client")
	}
	if client.Revoked {
		return echo.NewHTTPError(http.StatusConflict, "this client has been revoked")
	}
	// Checked before the row is written so the common case -- a machine that is
	// simply not up -- is a clean rejection rather than a row that fails a moment
	// later. The race with a disconnect between here and Send is handled below.
	if !h.hub.Connected(clientID) {
		return echo.NewHTTPError(http.StatusConflict, "this client is not connected")
	}

	// Best effort: an admin's spec is not bounded by a quota, so an unparseable
	// size is not worth refusing the create over -- it just does not count.
	diskMiB, _ := parseSizeMiB(spec.DiskSize)

	// Written before the job is pushed: a row with no job is a visible failure,
	// whereas a job with no row is a VM nobody knows about.
	params := db.CreateVMParams{
		ClientID:    pgClientID,
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
	insert := func(ctx context.Context, q *db.Queries, name string) error {
		spec.Name = name
		raw, err := json.Marshal(spec)
		if err != nil {
			return err
		}
		params.Name, params.Spec = name, raw
		row, err = q.CreateVM(ctx, params)
		if err != nil || ttl == 0 {
			return err
		}
		// Written in the same transaction as the row, for the reason spelled out on
		// the self-service path: the deadline lives only here, so a row without its
		// task is a sandbox nothing will ever destroy.
		_, err = scheduleTask(ctx, q, scheduleTaskParams{
			Kind:        taskVMExpire,
			SubjectKind: subjectVM,
			SubjectID:   row.ID,
			Payload:     vmExpirePayload{VMName: name, TTLSeconds: req.TTLSeconds},
			Reason:      fmt.Sprintf("temporary sandbox: created with a %s ttl", formatTTL(ttl)),
			CreatedBy:   params.CreatedBy,
			After:       ttl,
			MaxAttempts: taskExpireAttempts,
		})
		return err
	}
	// A transaction per name attempt: a unique violation aborts the one it happens
	// in, so a redrawn name needs a fresh transaction to insert under.
	if err := withVMName(ctx, name, func(ctx context.Context, name string) error {
		if ttl == 0 {
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

	// The row id is the correlation id, so the client's result finds this row
	// without the server holding any in-flight state of its own.
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
		return echo.NewHTTPError(http.StatusConflict, "could not deliver the job to this client")
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
// four checks -- the row exists, the host assigned it an id, the client is live,
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
	// The client treats every action as idempotent, so these guards are about
	// telling the caller its request made no sense rather than about safety.
	// 'stale' is not checked: a row nobody has heard from recently is exactly one
	// an operator may need to act on.
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

	// The row id is the correlation id, exactly as for a create, so the result
	// settles this row without the server tracking in-flight jobs.
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

	// A destroy makes any pending expiry moot -- the task checks the VM's status
	// first and would cancel itself anyway, so this is about not leaving the queue
	// full of expiries for VMs that are already gone.
	if kind == proto.KindVMDestroy {
		cancelTasksForSubject(ctx, h.q, subjectVM, row.ID,
			"the vm was destroyed before its ttl ran out")
	}

	// 202: the guest is given time to shut down cleanly, so the row settles when
	// the client reports back rather than by the time this returns.
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
	ctx := c.Request().Context()
	if err := h.q.DeleteVM(ctx, pgID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not delete vm")
	}
	// The row is gone, so an expiry against it has nothing to read. Cancelled
	// rather than left to work that out on its own run, for the same reason as
	// everywhere else: the pending queue should list work that will happen.
	//
	// Note this deletes the record but not the guest, so a VM re-adopted from its
	// host's next inventory report comes back without a TTL. That is the honest
	// outcome -- the deadline was on the row somebody deleted, and inventing a new
	// one for an adopted VM would be the control plane making up a promise.
	cancelTasksForSubject(ctx, h.q, subjectVM, pgID, "the vm record was deleted")
	return c.NoContent(http.StatusNoContent)
}

func (h *AdminHandler) failVM(ctx context.Context, id pgtype.UUID, msg string) {
	if err := h.q.MarkVMFailed(ctx, db.MarkVMFailedParams{ID: id, LastError: msg}); err != nil {
		log.Printf("could not mark vm failed: %v", err)
	}
}
