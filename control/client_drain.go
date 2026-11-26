package main

import (
	"context"
	"errors"
	"log"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

const (
	taskClientDrain = "client.drain"
	subjectClient   = "client"

	clientDrainRetry   = time.Minute
	clientDrainTimeout = vmEvictWait + 30*time.Second
)

type deletePlanVM struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Reason string `json:"reason,omitempty"`
}

// clientDeletePlan is what deleting a host does to its vms. Deleted are gone for
// good; forgotten are fsx vms control holds no volume for, whose record goes
// but whose disk is left alone; moved go to other hosts; blocked keep the host
// from being deleted.
type clientDeletePlan struct {
	Deleted   []deletePlanVM `json:"deleted"`
	Forgotten []deletePlanVM `json:"forgotten"`
	Moved     []deletePlanVM `json:"moved"`
	Blocked   []deletePlanVM `json:"blocked"`

	// unhook is every fsx copy the host holds, which it is told to shut down.
	unhook []db.Vm
}

func planClientDelete(ctx context.Context, q *db.Queries, clientID pgtype.UUID) (clientDeletePlan, error) {
	plan := clientDeletePlan{Deleted: []deletePlanVM{}, Forgotten: []deletePlanVM{}, Moved: []deletePlanVM{}, Blocked: []deletePlanVM{}}
	vms, err := q.ListAllVMsByClient(ctx, clientID)
	if err != nil {
		return plan, err
	}
	for _, vm := range vms {
		item := deletePlanVM{ID: uuid.UUID(vm.ID.Bytes).String(), Name: vm.Name}
		_, err := q.GetFSxDiskByVM(ctx, vm.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return plan, err
		}
		hasVolume := err == nil
		switch {
		case !hasVolume && vm.DiskBackend != diskBackendFSx:
			plan.Deleted = append(plan.Deleted, item)
			continue
		case !hasVolume:
			plan.Forgotten = append(plan.Forgotten, item)
		case vm.HostMovingTo.Valid:
			plan.Moved = append(plan.Moved, item)
		default:
			if item.Reason = hostMoveBlocker(vm, true); item.Reason != "" {
				plan.Blocked = append(plan.Blocked, item)
				continue
			}
			plan.Moved = append(plan.Moved, item)
		}
		if vm.VMID != "" && !vm.HostMovingTo.Valid {
			plan.unhook = append(plan.unhook, vm)
		}
	}
	return plan, nil
}

func (p clientDeletePlan) needsDrain() bool {
	return len(p.unhook) > 0 || len(p.Moved) > 0
}

// @Summary     Preview deleting a host
// @Description Lists what deleting the host does to its VMs: local-disk VMs are deleted for good, FSx VMs move to other hosts, and any FSx VM that cannot move yet blocks the delete.
// @Tags        admin
// @Produce     json
// @Security    BearerAuth
// @Param       id path string true "client id" format(uuid)
// @Success     200 {object} clientDeletePlan
// @Router      /admin/clients/{id}/delete-plan [get]
func (h *AdminHandler) ClientDeletePlan(c *echo.Context) error {
	pgID, err := parseUUID(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid client id")
	}
	plan, err := planClientDelete(c.Request().Context(), h.q, pgID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the host's vms")
	}
	return c.JSON(http.StatusOK, plan)
}

// drainClient revokes a host and deletes the vms that cannot move now, leaving
// the row until a client.drain has unhooked and moved every fsx vm off it. The
// host stays connected so the drain can tell it to let go of the volumes.
func (h *AdminHandler) drainClient(ctx context.Context, client db.Client, plan clientDeletePlan) error {
	return inTx(ctx, h.pool, h.q, func(q *db.Queries) error {
		n, err := q.MarkClientDeleting(ctx, client.ID)
		if err != nil || n == 0 {
			return err
		}
		for _, vm := range plan.unhook {
			if err := q.InsertVMHostEviction(ctx, db.InsertVMHostEvictionParams{
				MachineID: client.MachineID, VMID: vm.VMID, Vm: vm.ID,
			}); err != nil {
				return err
			}
		}
		if err := q.DeleteVMsWithoutVolumeByClient(ctx, client.ID); err != nil {
			return err
		}
		_, err = scheduleTask(ctx, q, scheduleTaskParams{
			Kind:        taskClientDrain,
			SubjectKind: subjectClient,
			SubjectID:   client.ID,
			Reason:      "move the fsx vms off a deleted host, then delete it",
			MaxAttempts: math.MaxInt32,
		})
		return err
	})
}

// handleClientDrain first has a still connected host shut down every fsx copy it
// holds and unmount the volumes, so none stays locked, then disconnects it. It
// hands each fsx vm to the least loaded connected host, and deletes the host
// once none are left. With no host to move to it keeps waiting; the disks are
// safe on FSx meanwhile.
func handleClientDrain(ctx context.Context, r *taskRunner, t db.ScheduledTask) taskOutcome {
	client, err := r.q.GetClientByID(ctx, t.SubjectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return taskDone("the host is already gone")
	}
	if err != nil {
		return taskRetry(clientDrainRetry, "could not read the host: %v", err)
	}
	if !client.Deleting {
		return taskCancelled("%s is no longer being deleted", client.Hostname)
	}
	if id := uuid.UUID(client.ID.Bytes).String(); r.hub.Connected(id) {
		r.unhookClient(ctx, client)
		r.hub.Kick(id, "client deleted")
	}
	vms, err := r.q.ListAllVMsByClient(ctx, client.ID)
	if err != nil {
		return taskRetry(clientDrainRetry, "could not list the host's vms: %v", err)
	}
	hosts, err := r.q.ListAvailableHosts(ctx)
	if err != nil {
		return taskRetry(clientDrainRetry, "could not list hosts: %v", err)
	}
	targets := hosts[:0]
	for _, h := range hosts {
		if h.ID != client.ID && r.hub.Connected(uuid.UUID(h.ID.Bytes).String()) {
			targets = append(targets, h)
		}
	}

	left, next := 0, 0
	for _, vm := range vms {
		if vm.DiskBackend != diskBackendFSx {
			continue
		}
		left++
		if vm.HostMovingTo.Valid || len(targets) == 0 {
			continue
		}
		if msg := hostMoveBlocker(vm, true); msg != "" {
			log.Printf("drain %s: cannot move %s yet: %s", client.Hostname, vm.Name, msg)
			continue
		}
		to := targets[next%len(targets)]
		next++
		if _, err := beginHostMove(ctx, r.q, vm, to.ID, to.Hostname, true, vm.Status == "running", t.CreatedBy); err != nil {
			log.Printf("drain %s: could not move %s: %v", client.Hostname, vm.Name, err)
		}
	}
	if left > 0 {
		if len(targets) == 0 {
			return taskRetry(clientDrainRetry, "%d fsx vm(s) wait for another host to connect", left)
		}
		return taskRetry(clientDrainRetry, "%d fsx vm(s) are moving off %s", left, client.Hostname)
	}
	if err := r.q.DeleteClient(ctx, client.ID); err != nil {
		return taskRetry(clientDrainRetry, "could not delete the host: %v", err)
	}
	return taskDone("deleted %s once its fsx vms had moved off", client.Hostname)
}

// unhookClient evicts every copy recorded against the host at once. Any that
// fails stays recorded and is retried if the machine enrolls again.
func (r *taskRunner) unhookClient(ctx context.Context, client db.Client) {
	evictions, err := r.q.ListVMHostEvictions(ctx, client.MachineID)
	if err != nil {
		log.Printf("drain %s: could not list what to unhook: %v", client.Hostname, err)
		return
	}
	id := uuid.UUID(client.ID.Bytes).String()
	var wg sync.WaitGroup
	for _, e := range evictions {
		wg.Go(func() {
			if err := askEvict(ctx, r.q, r.hub, id, client.MachineID, e.VMID); err != nil {
				log.Printf("drain %s: could not unhook vm %s: %v", client.Hostname, e.VMID, err)
			}
		})
	}
	wg.Wait()
}
