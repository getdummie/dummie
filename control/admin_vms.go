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

type vmDTO struct {
	ID        string `json:"id"`
	ClientID  string `json:"client_id"`
	VMID      string `json:"vm_id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Boot      string `json:"boot"`
	CPUs      int32  `json:"cpus"`
	MemoryMiB int32  `json:"memory_mib"`
	DiskMiB   int32  `json:"disk_mib"`
	IP        string `json:"ip"`

	DefaultPort int32   `json:"default_port"`
	PublicPorts []int32 `json:"public_ports"`

	Spec      json.RawMessage `json:"spec" swaggertype:"object"`
	LastError string          `json:"last_error"`
	CreatedAt string          `json:"created_at"`
	StartedAt string          `json:"started_at"`

	CreatedBy string `json:"created_by"`

	URL string `json:"url"`

	ConsoleURL string `json:"console_url"`

	DesktopURL string `json:"desktop_url"`

	ExpiresAt string `json:"expires_at"`

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

func (h *AdminHandler) ListVMTargets(c *echo.Context) error {
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid vm id")
	}
	ctx := c.Request().Context()
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

type adminCreateVMReq struct {
	proto.VMSpec
	DefaultPort int32   `json:"default_port"`
	PublicPorts []int32 `json:"public_ports"`
	TTLSeconds int64 `json:"ttl_seconds"`
}

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
	name, err := validateVMName(spec.Name)
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
	if !h.hub.Connected(clientID) {
		return echo.NewHTTPError(http.StatusConflict, "this client is not connected")
	}

	diskMiB, _ := parseSizeMiB(spec.DiskSize)

	params := db.CreateVMParams{
		ClientID:    pgClientID,
		Boot:        spec.Boot,
		CPUs:        int32(spec.CPUs),
		MemoryMiB:   int32(spec.Memory),
		DiskMiB:     diskMiB,
		DefaultPort: defaultPort,
		PublicPorts: publicPorts,
	}
	if uid, _ := c.Get("uid").(string); uid != "" {
		if pgID, err := parseUUID(uid); err == nil {
			params.CreatedBy = pgID
		}
	}
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

func (h *AdminHandler) StopVM(c *echo.Context) error {
	return h.actOnVM(c, proto.KindVMStop)
}

func (h *AdminHandler) StartVM(c *echo.Context) error {
	return h.actOnVM(c, proto.KindVMStart)
}

func (h *AdminHandler) DestroyVM(c *echo.Context) error {
	return h.actOnVM(c, proto.KindVMDestroy)
}

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

func (h *AdminHandler) DeleteVM(c *echo.Context) error {
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid vm id")
	}
	ctx := c.Request().Context()
	if err := h.q.DeleteVM(ctx, pgID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not delete vm")
	}
	cancelTasksForSubject(ctx, h.q, subjectVM, pgID, "the vm record was deleted")
	return c.NoContent(http.StatusNoContent)
}

func (h *AdminHandler) failVM(ctx context.Context, id pgtype.UUID, msg string) {
	if err := h.q.MarkVMFailed(ctx, db.MarkVMFailedParams{ID: id, LastError: msg}); err != nil {
		log.Printf("could not mark vm failed: %v", err)
	}
}
