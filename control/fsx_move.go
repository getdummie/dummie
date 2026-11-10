package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"

	"control/internal/db"
	"control/internal/proto"
)

const (
	taskVMDiskMove = "vm.disk_move"

	vmDiskMoveAttempts = 3
	diskDropLocalWait  = 30 * time.Second
)

// startDiskMove marks a stopped local vm as moving and queues the task that
// copies its disk onto a new FSx volume.
func startDiskMove(ctx context.Context, q *db.Queries, vm db.Vm, by pgtype.UUID) (db.Vm, error) {
	if err := diskBackendAllowed(ctx, q, diskBackendFSx); err != nil {
		return vm, err
	}
	switch {
	case vm.DiskBackend != diskBackendLocal:
		return vm, echo.NewHTTPError(http.StatusConflict, "this vm's disk is already on FSx")
	case vm.DiskMoving:
		return vm, echo.NewHTTPError(http.StatusConflict, "this vm's disk is already being moved")
	case vm.VMID == "" || vm.Status != "stopped":
		return vm, echo.NewHTTPError(http.StatusConflict, "stop the vm before moving its disk")
	case vm.DiskMiB <= 0:
		return vm, echo.NewHTTPError(http.StatusConflict, "this vm has no recorded disk size; resize it first")
	}
	row, err := q.BeginVMDiskMove(ctx, vm.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return vm, echo.NewHTTPError(http.StatusConflict, "this vm changed; reload and try again")
	}
	if err != nil {
		return vm, echo.NewHTTPError(http.StatusInternalServerError, "could not start the move")
	}
	if _, err := scheduleTask(ctx, q, scheduleTaskParams{
		Kind:        taskVMDiskMove,
		SubjectKind: subjectVM,
		SubjectID:   vm.ID,
		Reason:      "move the vm's local disk onto FSx",
		CreatedBy:   by,
		MaxAttempts: vmDiskMoveAttempts,
	}); err != nil {
		_ = q.FailVMDiskMove(ctx, db.FailVMDiskMoveParams{ID: vm.ID, LastError: "could not queue the disk move"})
		return vm, echo.NewHTTPError(http.StatusInternalServerError, "could not queue the disk move")
	}
	return row, nil
}

// handleVMDiskMove creates an empty volume for the vm, has its host copy the
// disk onto it, and only then switches the vm to FSx.
func handleVMDiskMove(ctx context.Context, r *taskRunner, t db.ScheduledTask) taskOutcome {
	vm, err := r.q.GetVM(ctx, t.SubjectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return taskCancelled("the vm was deleted before its disk moved")
	}
	if err != nil {
		return taskRetry(vmDiskFSxRetry, "could not read the vm: %v", err)
	}
	if !vm.DiskMoving {
		return taskCancelled("%s has no disk move pending", vm.Name)
	}
	// discard is only safe once the host has said no: an unanswered ask may
	// have left the vm booting from the volume.
	failWith := func(discard bool, format string, args ...any) taskOutcome {
		msg := fmt.Sprintf(format, args...)
		failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		if discard {
			discardMoveVolume(failCtx, r.q, vm)
		}
		if err := r.q.FailVMDiskMove(failCtx, db.FailVMDiskMoveParams{
			ID: vm.ID, LastError: "could not move the disk to FSx: " + msg,
		}); err != nil {
			log.Printf("could not record the failed disk move of %s: %v", vm.Name, err)
		}
		return taskFailed("%s", msg)
	}
	retryOrFail := func(format string, args ...any) taskOutcome {
		if t.Attempts >= t.MaxAttempts {
			return failWith(false, format, args...)
		}
		return taskRetry(vmDiskFSxRetry, format, args...)
	}

	store, err := openFSx(ctx, r.q)
	if err != nil {
		return retryOrFail("%v", err)
	}
	clientID := uuid.UUID(vm.ClientID.Bytes).String()
	if !r.hub.Connected(clientID) {
		return retryOrFail("the host of %s is not connected", vm.Name)
	}

	size := int64(vm.DiskMiB) << 20
	disk, err := r.q.GetFSxDiskByVM(ctx, vm.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		vmID := uuid.UUID(vm.ID.Bytes).String()
		vol, cerr := store.createVolume(ctx, uuid.UUID(t.ID.Bytes).String(), "vm-"+vmID, size, "")
		if cerr == nil {
			vol, cerr = store.waitVolume(ctx, aws.ToString(vol.VolumeId))
		}
		if cerr != nil {
			return retryOrFail("%v", cerr)
		}
		disk, err = r.q.CreateFSxDisk(ctx, db.CreateFSxDiskParams{
			Vm:         vm.ID,
			VolumeID:   aws.ToString(vol.VolumeId),
			VolumePath: volumePath(vol),
		})
	}
	if err != nil {
		return retryOrFail("could not record the disk volume: %v", err)
	}
	if disk.Released {
		return retryOrFail("the volume of an earlier move is still being deleted")
	}

	res, err := r.hub.Ask(ctx, clientID, proto.Job{
		Kind: proto.KindDiskMove,
		VMID: vm.VMID,
		Move: &proto.DiskMove{FSx: store.disk(disk.VolumePath), SizeBytes: size},
	}, fsxHostJobWait)
	if err != nil {
		return retryOrFail("could not ask the host to copy the disk: %v", err)
	}
	if !res.OK {
		return failWith(true, "%s", res.Error)
	}
	if err := r.q.FinishVMDiskMove(ctx, vm.ID); err != nil {
		return retryOrFail("could not record the move: %v", err)
	}
	return taskDone("moved the disk of %s onto %s", vm.Name, disk.VolumeID)
}

// discardMoveVolume deletes the volume a failed move made, or leaves it to the
// fsx sweep when FSx will not take the delete now.
func discardMoveVolume(ctx context.Context, q *db.Queries, vm db.Vm) {
	disk, err := q.GetFSxDiskByVM(ctx, vm.ID)
	if err != nil {
		return
	}
	if store, err := openFSx(ctx, q); err == nil {
		if err := store.deleteVolume(ctx, disk.VolumeID); err == nil {
			if err := q.DeleteFSxDisk(ctx, disk.ID); err == nil {
				return
			}
		}
	}
	if err := q.ReleaseFSxDiskForVM(ctx, vm.ID); err != nil {
		log.Printf("could not release the volume of the failed move of %s: %v", vm.Name, err)
	}
}

// @Summary     Move a VM's disk onto FSx
// @Description Copies a stopped VM's local disk onto its own FSx for OpenZFS volume and boots it from there from then on. The copy runs in the background; disk_moving is true until it is done, and last_error says why when it fails. The local disk is kept on the host until an admin deletes it.
// @Tags        vms
// @Produce     json
// @Security    BearerAuth
// @Param       id path string true "vm id" format(uuid)
// @Success     202 {object} vmDTO
// @Failure     401 {object} apiError
// @Failure     403 {object} apiError "FSx disks are not enabled"
// @Failure     404 {object} apiError
// @Failure     409 {object} apiError "the vm is not stopped, already on FSx, already moving, or has no disk size"
// @Router      /vms/{id}/move-disk [post]
func (h *UserHandler) MoveDisk(c *echo.Context) error {
	vm, err := h.ownedVM(c)
	if err != nil {
		return err
	}
	owner, err := callerID(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "not signed in")
	}
	row, err := startDiskMove(c.Request().Context(), h.q, vm, owner)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusAccepted, toVMDTO(row))
}

func (h *AdminHandler) MoveDisk(c *echo.Context) error {
	vm, err := h.vmByParam(c)
	if err != nil {
		return err
	}
	var by pgtype.UUID
	if uid, _ := c.Get("uid").(string); uid != "" {
		by, _ = parseUUID(uid)
	}
	row, err := startDiskMove(c.Request().Context(), h.q, vm, by)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusAccepted, toVMDTO(row))
}

// DropLocalDisk deletes the local disk a vm left on its host when it moved to FSx.
func (h *AdminHandler) DropLocalDisk(c *echo.Context) error {
	vm, err := h.vmByParam(c)
	if err != nil {
		return err
	}
	if !vm.LocalDiskKept {
		return echo.NewHTTPError(http.StatusConflict, "this vm has no local disk left behind")
	}
	ctx := c.Request().Context()
	clientID := uuid.UUID(vm.ClientID.Bytes).String()
	if !h.hub.Connected(clientID) {
		return echo.NewHTTPError(http.StatusConflict, "the host of this vm is not connected")
	}
	res, err := h.hub.Ask(ctx, clientID, proto.Job{Kind: proto.KindDiskDropLocal, VMID: vm.VMID}, diskDropLocalWait)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, "the host did not answer")
	}
	if !res.OK {
		return echo.NewHTTPError(http.StatusBadGateway, res.Error)
	}
	if err := h.q.ClearVMLocalDiskKept(ctx, vm.ID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "the disk is gone but could not be recorded")
	}
	vm.LocalDiskKept = false
	return c.JSON(http.StatusOK, toVMDTO(vm))
}

func (h *AdminHandler) vmByParam(c *echo.Context) (db.Vm, error) {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		return db.Vm{}, echo.NewHTTPError(http.StatusBadRequest, "invalid vm id")
	}
	vm, err := h.q.GetVM(c.Request().Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Vm{}, echo.NewHTTPError(http.StatusNotFound, "no such vm")
	}
	if err != nil {
		return db.Vm{}, echo.NewHTTPError(http.StatusInternalServerError, "could not read vm")
	}
	return vm, nil
}
