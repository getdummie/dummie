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

	// Offline moves off a host that is not connected, from the boot record it
	// last reported, after shutting it out of the volume.
	Offline bool `json:"offline,omitempty"`
	Start   bool `json:"start,omitempty"`
}

type moveHostReq struct {
	ClientID string `json:"client_id"`
}

func startHostMove(ctx context.Context, q *db.Queries, hub *Hub, vm db.Vm, target string, by pgtype.UUID) (db.Vm, error) {
	from := uuid.UUID(vm.ClientID.Bytes).String()
	offline := !hub.Connected(from)
	if msg := hostMoveBlocker(vm, offline); msg != "" {
		return vm, echo.NewHTTPError(http.StatusConflict, msg)
	}
	to, err := parseUUID(strings.TrimSpace(target))
	if err != nil {
		return vm, echo.NewHTTPError(http.StatusBadRequest, "choose a host to move this vm to")
	}
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
	if !hub.Connected(uuid.UUID(to.Bytes).String()) {
		return vm, echo.NewHTTPError(http.StatusConflict, "that host is not connected")
	}
	row, err := beginHostMove(ctx, q, vm, to, client.Hostname, offline, false, by)
	if errors.Is(err, errHostMoveRaced) {
		return vm, echo.NewHTTPError(http.StatusConflict, "this vm changed; reload and try again")
	}
	if err != nil {
		return vm, echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return row, nil
}

// hostMoveBlocker says why vm cannot leave its host now, or "" when it can.
func hostMoveBlocker(vm db.Vm, offline bool) string {
	switch {
	case vm.DiskBackend != diskBackendFSx:
		return "only a vm whose disk is on FSx can change host"
	case vm.DiskMoving:
		return "this vm's disk is still being moved to FSx"
	case vm.HostMovingTo.Valid:
		return "this vm is already changing host"
	case vm.VMID == "":
		return "this vm is not on a host yet"
	case !vm.KernelID.Valid:
		return "this vm's kernel is not from the catalogue, so another host cannot fetch it"
	case !offline && vm.Status != "stopped":
		return "stop the vm before changing its host"
	case offline && vm.Status != "stopped" && vm.Status != "running":
		return "this vm is " + vm.Status + ", so it cannot change host"
	case offline && len(vm.BootRecord) == 0:
		return "this vm's host is offline and never reported how to boot the vm elsewhere"
	}
	return ""
}

// beginHostMove marks the vm as moving and queues the move. start boots it on
// the new host once it is there.
func beginHostMove(ctx context.Context, q *db.Queries, vm db.Vm, to pgtype.UUID, hostname string, offline, start bool, by pgtype.UUID) (db.Vm, error) {
	newID, err := newHostVMID()
	if err != nil {
		return vm, errors.New("could not pick an id for the vm")
	}
	row, err := q.BeginVMHostMove(ctx, db.BeginVMHostMoveParams{ID: vm.ID, HostMovingTo: to, Offline: offline})
	if errors.Is(err, pgx.ErrNoRows) {
		return vm, errHostMoveRaced
	}
	if err != nil {
		return vm, errors.New("could not start the move")
	}
	payload := vmHostMovePayload{
		From: uuid.UUID(vm.ClientID.Bytes).String(), FromVMID: vm.VMID,
		To: uuid.UUID(to.Bytes).String(), ToVMID: newID,
		Offline: offline, Start: start,
	}
	if _, err := scheduleTask(ctx, q, scheduleTaskParams{
		Kind:        taskVMHostMove,
		SubjectKind: subjectVM,
		SubjectID:   vm.ID,
		Payload:     payload,
		Reason:      "move the vm to " + hostname,
		CreatedBy:   by,
		MaxAttempts: vmHostMoveAttempts,
	}); err != nil {
		_ = q.FailVMHostMove(ctx, db.FailVMHostMoveParams{ID: vm.ID, LastError: "could not queue the host move"})
		return vm, errors.New("could not queue the host move")
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
// An offline old host cannot let go, so it is shut out of the volume instead.
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
	disk, err := r.q.GetFSxDiskByVM(ctx, vm.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return failWith("control holds no FSx volume for this vm, so it cannot hand it to another host")
	}
	if err != nil {
		return retryOrFail("could not read the vm's volume: %v", err)
	}
	fence := p.Offline || disk.NFSClient != ""
	if p.Offline {
		fail := failWith
		failWith = func(format string, args ...any) taskOutcome {
			r.undoOfflineMove(ctx, disk, p)
			return fail(format, args...)
		}
	}
	if !r.hub.Connected(p.To) {
		return retryOrFail("the new host has to be connected")
	}
	if !p.Offline && !r.hub.Connected(p.From) {
		return retryOrFail("both hosts have to be connected")
	}

	var rec *proto.VMRecord
	if p.Offline {
		if rec, err = lastBootRecord(vm, disk); err != nil {
			return failWith("%v", err)
		}
	} else {
		rec, err = r.askRecord(ctx, p.From, proto.Job{Kind: proto.KindVMExport, VMID: p.FromVMID}, hostMoveJobWait)
		if err != nil {
			return retryOrFail("could not read the vm from its host: %v", err)
		}
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
		if fence && got.NFSClient == "" {
			return retryOrFail("the new host could not tell its address towards FSx")
		}
		return r.switchHost(ctx, vm, disk, p, from, to, got, fence, retryOrFail, failWith)
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
	got := res.Record
	if fence && got.NFSClient == "" {
		r.releaseQuietly(ctx, p.To, p.ToVMID)
		return failWith("the new host could not tell its address towards FSx")
	}

	if !p.Offline {
		res, err = r.hub.Ask(ctx, p.From, proto.Job{Kind: proto.KindVMRelease, VMID: p.FromVMID}, hostMoveJobWait)
		if err != nil {
			return retryOrFail("could not ask the old host to let go of the vm: %v", err)
		}
		if !res.OK {
			r.releaseQuietly(ctx, p.To, p.ToVMID)
			return failWith("the old host would not let go of the vm: %s", res.Error)
		}
	}
	return r.switchHost(ctx, vm, disk, p, from, to, got, fence, retryOrFail, failWith)
}

// lastBootRecord rebuilds what an offline host would have exported. The sizes
// come from the row, which a resize the host never reported back still updated.
func lastBootRecord(vm db.Vm, disk db.FsxDisk) (*proto.VMRecord, error) {
	var rec proto.VMRecord
	if len(vm.BootRecord) == 0 || json.Unmarshal(vm.BootRecord, &rec) != nil || rec.FSx == nil {
		return nil, errors.New("its host never reported how to boot it elsewhere")
	}
	if rec.FSx.Path != disk.VolumePath {
		return nil, fmt.Errorf("its host last reported it on %s, not on its volume %s", rec.FSx.Path, disk.VolumePath)
	}
	rec.CPUs, rec.MemoryMiB = int(vm.CPUs), int(vm.MemoryMiB)
	rec.IP, rec.NFSClient = "", ""
	return &rec, nil
}

// undoOfflineMove gives the volume back to whoever could mount it before, so a
// failed move leaves the vm bootable where it was.
func (r *taskRunner) undoOfflineMove(ctx context.Context, disk db.FsxDisk, p vmHostMovePayload) {
	ctx = context.WithoutCancel(ctx)
	r.releaseQuietly(ctx, p.To, p.ToVMID)
	store, err := openFSx(ctx, r.q)
	if err != nil {
		log.Printf("could not give %s back to its old host: %v", disk.VolumeID, err)
		return
	}
	clients := disk.NFSClient
	if clients == "" {
		clients = store.clients
	}
	if err := store.setVolumeClients(ctx, disk.VolumeID, clients); err != nil {
		log.Printf("could not give %s back to its old host: %v", disk.VolumeID, err)
	}
}

var errHostMoveRaced = errors.New("the vm changed while it was being moved")

func (r *taskRunner) switchHost(ctx context.Context, vm db.Vm, disk db.FsxDisk, p vmHostMovePayload, from, to pgtype.UUID,
	got *proto.VMRecord, fence bool, retryOrFail, failWith func(string, ...any) taskOutcome) taskOutcome {
	if fence {
		store, err := openFSx(ctx, r.q)
		if err != nil {
			return retryOrFail("could not reach FSx: %v", err)
		}
		if err := store.setVolumeClients(ctx, disk.VolumeID, got.NFSClient); err != nil {
			return retryOrFail("%v", err)
		}
	}
	err := inTx(ctx, r.pool, r.q, func(q *db.Queries) error {
		// The new host's inventory may have adopted the vm as a row of its own.
		if err := q.DeleteAdoptedVM(ctx, db.DeleteAdoptedVMParams{ClientID: to, VMID: p.ToVMID, ID: vm.ID}); err != nil {
			return err
		}
		n, err := q.SwitchVMHost(ctx, db.SwitchVMHostParams{
			ID:         vm.ID,
			ToClient:   to,
			ToVMID:     p.ToVMID,
			IP:         got.IP,
			FromClient: from,
			FromVMID:   p.FromVMID,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return errHostMoveRaced
		}
		if fence {
			if err := q.SetFSxDiskNFSClient(ctx, db.SetFSxDiskNFSClientParams{Vm: vm.ID, NFSClient: got.NFSClient}); err != nil {
				return err
			}
		}
		if p.Offline {
			old, err := q.GetClientByID(ctx, from)
			if err != nil {
				return err
			}
			return q.InsertVMHostEviction(ctx, db.InsertVMHostEvictionParams{MachineID: old.MachineID, VMID: p.FromVMID, Vm: vm.ID})
		}
		return nil
	})
	if errors.Is(err, errHostMoveRaced) {
		return failWith("%v", err)
	}
	if err != nil {
		return retryOrFail("could not record the new host: %v", err)
	}
	for _, id := range []pgtype.UUID{from, to} {
		r.pushHostConfigs(ctx, id)
	}
	if p.Start {
		r.startMovedVM(ctx, vm, p)
	}
	return taskDone("moved %s from %s to %s as %s", vm.Name, p.From, p.To, p.ToVMID)
}

// startMovedVM boots a vm that was running on the host it was moved off. Its
// result settles like any start, against the row id the job carries.
func (r *taskRunner) startMovedVM(ctx context.Context, vm db.Vm, p vmHostMovePayload) {
	env, err := proto.NewEnvelope(proto.TypeJob, uuid.UUID(vm.ID.Bytes).String(), proto.Job{
		Kind: proto.KindVMStart,
		VMID: p.ToVMID,
	})
	if err == nil {
		err = r.hub.Send(p.To, env)
	}
	if err != nil {
		log.Printf("could not start %s on its new host: %v", vm.Name, err)
		_ = r.q.SetVMLastError(ctx, db.SetVMLastErrorParams{ID: vm.ID, LastError: "moved, but could not be started on its new host"})
	}
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
// @Description Hands a VM whose disk is on FSx over to another connected host. When its current host is connected the VM has to be stopped. When it is not, the VM is moved from what that host last reported, running or not: the old host is shut out of the volume, and deletes its copy when it reconnects. The move runs in the background; host_moving is true until it is done, and last_error says why when it fails. The VM gets a new address on the new host, and its URL follows that host's domain.
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
// @Failure     409 {object} apiError "the vm is not stopped, not on FSx, already moving, or the new host is not connected"
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
