package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"

	"control/internal/db"
	"control/internal/proto"
)

const (
	taskVMHostMove = "vm.host_move"

	vmHostMoveAttempts = 5
	vmHostMoveRetry    = 30 * time.Second
	hostMoveJobWait    = time.Minute
	hostAttachJobWait  = 5 * time.Minute

	taskVMHostMoveTimeout = hostAttachJobWait + 4*hostMoveJobWait
)

// vmHostMovePayload pins both ends of the move, so a retry after the row has
// switched still knows which host to clean up and asks the new one for the
// same local id rather than making a second vm.
type vmHostMovePayload struct {
	From     string `json:"from"`
	FromVMID string `json:"from_vm_id"`
	To       string `json:"to"`
	ToVMID   string `json:"to_vm_id"`
}

type moveHostReq struct {
	ClientID string `json:"client_id"`
}

func startHostMove(ctx context.Context, q *db.Queries, hub *Hub, vm db.Vm, target string, by pgtype.UUID) (db.Vm, error) {
	switch {
	case vm.DiskBackend != diskBackendFSx:
		return vm, echo.NewHTTPError(http.StatusConflict, "only a vm whose disk is on FSx can change host")
	case vm.DiskMoving:
		return vm, echo.NewHTTPError(http.StatusConflict, "this vm's disk is still being moved to FSx")
	case vm.HostMovingTo.Valid:
		return vm, echo.NewHTTPError(http.StatusConflict, "this vm is already changing host")
	case vm.VMID == "" || vm.Status != "stopped":
		return vm, echo.NewHTTPError(http.StatusConflict, "stop the vm before changing its host")
	case !vm.KernelID.Valid:
		return vm, echo.NewHTTPError(http.StatusConflict, "this vm's kernel is not from the catalogue, so another host cannot fetch it")
	}
	to, err := parseUUID(strings.TrimSpace(target))
	if err != nil {
		return vm, echo.NewHTTPError(http.StatusBadRequest, "choose a host to move this vm to")
	}
	target = uuid.UUID(to.Bytes).String()
	if to == vm.ClientID {
		return vm, echo.NewHTTPError(http.StatusConflict, "this vm is already on that host")
	}
	client, err := q.GetClientByID(ctx, to)
	if errors.Is(err, pgx.ErrNoRows) {
		return vm, echo.NewHTTPError(http.StatusNotFound, "no such host")
	}
	if err != nil {
		return vm, echo.NewHTTPError(http.StatusInternalServerError, "could not read host")
	}
	if client.Revoked {
		return vm, echo.NewHTTPError(http.StatusConflict, "that host has been revoked")
	}
	if client.Role == clientRoleEdge {
		return vm, echo.NewHTTPError(http.StatusConflict, errEdgeRunsNoVMs)
	}
	from := uuid.UUID(vm.ClientID.Bytes).String()
	if !hub.Connected(from) {
		return vm, echo.NewHTTPError(http.StatusConflict, "this vm's current host is not connected; it has to be online to hand the vm over")
	}
	if !hub.Connected(target) {
		return vm, echo.NewHTTPError(http.StatusConflict, "that host is not connected")
	}
	newID, err := newHostVMID()
	if err != nil {
		return vm, echo.NewHTTPError(http.StatusInternalServerError, "could not pick an id for the vm")
	}

	row, err := q.BeginVMHostMove(ctx, db.BeginVMHostMoveParams{ID: vm.ID, HostMovingTo: to})
	if errors.Is(err, pgx.ErrNoRows) {
		return vm, echo.NewHTTPError(http.StatusConflict, "this vm changed; reload and try again")
	}
	if err != nil {
		return vm, echo.NewHTTPError(http.StatusInternalServerError, "could not start the move")
	}
	if _, err := scheduleTask(ctx, q, scheduleTaskParams{
		Kind:        taskVMHostMove,
		SubjectKind: subjectVM,
		SubjectID:   vm.ID,
		Payload:     vmHostMovePayload{From: from, FromVMID: vm.VMID, To: target, ToVMID: newID},
		Reason:      "move the vm to " + client.Hostname,
		CreatedBy:   by,
		MaxAttempts: vmHostMoveAttempts,
	}); err != nil {
		_ = q.FailVMHostMove(ctx, db.FailVMHostMoveParams{ID: vm.ID, LastError: "could not queue the host move"})
		return vm, echo.NewHTTPError(http.StatusInternalServerError, "could not queue the host move")
	}
	return row, nil
}

func newHostVMID() (string, error) {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// handleVMHostMove attaches the vm on the new host before the old one lets go
// of it, so at no point do both hosts hold it with the row unsure which is real.
func handleVMHostMove(ctx context.Context, r *taskRunner, t db.ScheduledTask) taskOutcome {
	vm, err := r.q.GetVM(ctx, t.SubjectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return taskCancelled("the vm was deleted before it changed host")
	}
	if err != nil {
		return taskRetry(vmHostMoveRetry, "could not read the vm: %v", err)
	}
	if !vm.HostMovingTo.Valid {
		return taskCancelled("%s has no host move pending", vm.Name)
	}
	failWith := func(format string, args ...any) taskOutcome {
		msg := fmt.Sprintf(format, args...)
		failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := r.q.FailVMHostMove(failCtx, db.FailVMHostMoveParams{
			ID: vm.ID, LastError: "could not change host: " + msg,
		}); err != nil {
			log.Printf("could not record the failed host move of %s: %v", vm.Name, err)
		}
		return taskFailed("%s", msg)
	}
	retryOrFail := func(format string, args ...any) taskOutcome {
		if t.Attempts >= t.MaxAttempts {
			return failWith(format, args...)
		}
		return taskRetry(vmHostMoveRetry, format, args...)
	}

	var p vmHostMovePayload
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return failWith("the task carried no usable payload")
	}
	from, err1 := parseUUID(p.From)
	to, err2 := parseUUID(p.To)
	if err1 != nil || err2 != nil || vm.HostMovingTo != to {
		return failWith("the task does not match the move recorded on the vm")
	}
	if vm.ClientID != from || vm.VMID != p.FromVMID {
		return failWith("the vm changed host while it was being moved")
	}
	if !r.hub.Connected(p.From) || !r.hub.Connected(p.To) {
		return retryOrFail("both hosts have to be connected")
	}

	rec, err := r.askRecord(ctx, p.From, proto.Job{Kind: proto.KindVMExport, VMID: p.FromVMID}, hostMoveJobWait)
	if err != nil {
		return retryOrFail("could not read the vm from its host: %v", err)
	}
	if rec == nil {
		// An earlier attempt got as far as releasing it; the new host has it.
		got, err := r.askRecord(ctx, p.To, proto.Job{Kind: proto.KindVMExport, VMID: p.ToVMID}, hostMoveJobWait)
		if err != nil {
			return retryOrFail("could not read the vm from its new host: %v", err)
		}
		if got == nil {
			return failWith("neither host has the vm any more; its disk is still on FSx")
		}
		return r.switchHost(ctx, vm, p, from, to, got.IP, retryOrFail, failWith)
	}

	kernel, err := r.q.GetKernel(ctx, vm.KernelID)
	if err != nil {
		return retryOrFail("could not read the vm's kernel: %v", err)
	}
	if r.blobs == nil {
		return failWith("%v", errNoBlobStore)
	}
	if rec.Kernel, err = r.blobs.PresignGet(ctx, kernel.ObjectKey, kernel.FileName); err != nil {
		return retryOrFail("could not prepare the kernel download: %v", err)
	}
	rec.ID = p.ToVMID

	res, err := r.hub.Ask(ctx, p.To, proto.Job{Kind: proto.KindVMAttach, Record: rec}, hostAttachJobWait)
	if err != nil {
		return retryOrFail("could not ask the new host to take the vm: %v", err)
	}
	if !res.OK || res.Record == nil {
		r.releaseQuietly(ctx, p.To, p.ToVMID)
		return failWith("the new host refused the vm: %s", res.Error)
	}
	ip := res.Record.IP

	res, err = r.hub.Ask(ctx, p.From, proto.Job{Kind: proto.KindVMRelease, VMID: p.FromVMID}, hostMoveJobWait)
	if err != nil {
		return retryOrFail("could not ask the old host to let go of the vm: %v", err)
	}
	if !res.OK {
		r.releaseQuietly(ctx, p.To, p.ToVMID)
		return failWith("the old host would not let go of the vm: %s", res.Error)
	}
	return r.switchHost(ctx, vm, p, from, to, ip, retryOrFail, failWith)
}

func (r *taskRunner) switchHost(ctx context.Context, vm db.Vm, p vmHostMovePayload, from, to pgtype.UUID, ip string,
	retryOrFail, failWith func(string, ...any) taskOutcome) taskOutcome {
	// The new host's inventory may have adopted the vm as a row of its own.
	if err := r.q.DeleteAdoptedVM(ctx, db.DeleteAdoptedVMParams{ClientID: to, VMID: p.ToVMID, ID: vm.ID}); err != nil {
		return retryOrFail("could not clear the adopted copy: %v", err)
	}
	n, err := r.q.SwitchVMHost(ctx, db.SwitchVMHostParams{
		ID:         vm.ID,
		ToClient:   to,
		ToVMID:     p.ToVMID,
		IP:         ip,
		FromClient: from,
		FromVMID:   p.FromVMID,
	})
	if err != nil {
		return retryOrFail("could not record the new host: %v", err)
	}
	if n == 0 {
		return failWith("the vm changed while it was being moved")
	}
	for _, id := range []pgtype.UUID{from, to} {
		r.pushHostConfigs(ctx, id)
	}
	return taskDone("moved %s from %s to %s as %s", vm.Name, p.From, p.To, p.ToVMID)
}

// askRecord returns a nil record, with no error, for a vm the host does not have.
func (r *taskRunner) askRecord(ctx context.Context, clientID string, job proto.Job, wait time.Duration) (*proto.VMRecord, error) {
	res, err := r.hub.Ask(ctx, clientID, job, wait)
	if err != nil {
		return nil, err
	}
	if !res.OK {
		return nil, errors.New(res.Error)
	}
	return res.Record, nil
}

func (r *taskRunner) releaseQuietly(ctx context.Context, clientID, vmID string) {
	res, err := r.hub.Ask(context.WithoutCancel(ctx), clientID, proto.Job{Kind: proto.KindVMRelease, VMID: vmID}, hostMoveJobWait)
	if err == nil && !res.OK {
		err = errors.New(res.Error)
	}
	if err != nil {
		log.Printf("could not undo the attach of %s on %s: %v", vmID, clientID, err)
	}
}

func (r *taskRunner) pushHostConfigs(ctx context.Context, clientID pgtype.UUID) {
	if r.certs != nil {
		pushProxyConfig(ctx, r.q, r.hub, r.certs.proxy, clientID)
		pushIntproxyConfig(ctx, r.q, r.blobs, r.hub, r.certs.proxy.controlURL, clientID)
	}
	pushDpipeConfig(ctx, r.q, r.blobs, r.hub, clientID)
	pushSuricataRules(ctx, r.q, r.hub, clientID)
	pushCoreDNSConfig(ctx, r.q, r.hub, clientID)
}

// @Summary     Move a VM to another host
// @Description Hands a stopped VM whose disk is on FSx over to another host. Both hosts have to be connected. The move runs in the background; host_moving is true until it is done, and last_error says why when it fails. The VM gets a new address on the new host, and its URL follows that host's domain.
// @Tags        vms
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       id   path string      true "vm id" format(uuid)
// @Param       body body moveHostReq true "the host to move to"
// @Success     202 {object} vmDTO
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError "no such vm or host"
// @Failure     409 {object} apiError "the vm is not stopped, not on FSx, already moving, or a host is not connected"
// @Router      /vms/{id}/move-host [post]
func (h *UserHandler) MoveHost(c *echo.Context) error {
	vm, err := h.ownedVM(c)
	if err != nil {
		return err
	}
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	var req moveHostReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	row, err := startHostMove(c.Request().Context(), h.q, h.hub, vm, req.ClientID, owner)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusAccepted, toVMDTO(row))
}

func (h *AdminHandler) MoveHost(c *echo.Context) error {
	vm, err := h.vmByParam(c)
	if err != nil {
		return err
	}
	var req moveHostReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	var by pgtype.UUID
	if uid, _ := c.Get("uid").(string); uid != "" {
		by, _ = parseUUID(uid)
	}
	row, err := startHostMove(c.Request().Context(), h.q, h.hub, vm, req.ClientID, by)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusAccepted, toVMDTO(row))
}
