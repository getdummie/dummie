package main

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"control/internal/db"
	"control/internal/proto"
)

const taskExpireRetry = 30 * time.Second

const taskExpireAttempts = 240

const taskCleanupEvery = 24 * time.Hour

const taskCertRenewEvery = 24 * time.Hour

type vmTargetExpirePayload struct {
	VMID        string `json:"vm_id"`
	VMName      string `json:"vm_name"`
	Destination string `json:"destination"`
	TTLSeconds  int64  `json:"ttl_seconds"`
}

type vmExpirePayload struct {
	VMName     string `json:"vm_name"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

func handleVMTargetExpire(ctx context.Context, r *taskRunner, t db.ScheduledTask) taskOutcome {
	if !t.SubjectID.Valid {
		return taskFailed("this task names no destination to expire")
	}
	row, err := r.q.GetVMNetworkTargetForExpiry(ctx, t.SubjectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return taskCancelled("the destination had already been removed, so there was nothing to expire")
		}
		return taskRetry(taskExpireRetry, "could not read the destination: %v", err)
	}

	n, err := r.q.DeleteVMNetworkTargetByID(ctx, t.SubjectID)
	if err != nil {
		return taskRetry(taskExpireRetry, "could not remove the destination: %v", err)
	}

	pushSuricataRules(ctx, r.q, r.hub, row.ClientID)
	pushCoreDNSConfig(ctx, r.q, r.hub, row.ClientID)

	if n == 0 {
		return taskDone("the destination %s was already gone; the host's policy was regenerated anyway", row.Destination)
	}
	return taskDone("withdrew %s from %s", row.Destination, row.VMName)
}

func handleVMExpire(ctx context.Context, r *taskRunner, t db.ScheduledTask) taskOutcome {
	if !t.SubjectID.Valid {
		return taskFailed("this task names no vm to expire")
	}
	vm, err := r.q.GetVM(ctx, t.SubjectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return taskCancelled("the vm record had already been deleted, so there was nothing to expire")
		}
		return taskRetry(taskExpireRetry, "could not read the vm: %v", err)
	}

	switch vm.Status {
	case "gone":
		return taskDone("%s is gone", vm.Name)
	case "failed":
		return taskCancelled("%s never started, so it had nothing to destroy", vm.Name)
	case "pending":
		return taskRetry(taskExpireRetry, "the create is still in flight, so there is nothing to destroy yet")
	}
	if vm.VMID == "" {
		return taskRetry(taskExpireRetry, "the host has not reported an id for this vm yet")
	}

	clientID := uuid.UUID(vm.ClientID.Bytes).String()
	if !r.hub.Connected(clientID) {
		return taskRetry(taskExpireRetry, "the host running %s is not connected, so the destroy is waiting", vm.Name)
	}

	env, err := proto.NewEnvelope(proto.TypeJob, uuid.UUID(vm.ID.Bytes).String(), proto.Job{
		Kind: proto.KindVMDestroy,
		VMID: vm.VMID,
	})
	if err != nil {
		return taskFailed("could not build the destroy job: %v", err)
	}
	if err := r.hub.Send(clientID, env); err != nil {
		return taskRetry(taskExpireRetry, "could not deliver the destroy to the host: %v", err)
	}
	log.Printf("task: asked host %s to destroy expired vm %s (%s)", clientID, vm.Name, vm.VMID)

	return taskRetry(taskExpireRetry, "asked the host to destroy %s; waiting for it to confirm", vm.Name)
}

func handleTaskCleanup(ctx context.Context, r *taskRunner, t db.ScheduledTask) taskOutcome {
	n, err := r.q.DeleteSettledScheduledTasksBefore(ctx, taskRetention.Seconds())
	if err != nil {
		return taskRetry(taskExpireRetry, "could not prune settled tasks: %v", err)
	}
	if _, err := scheduleTask(ctx, r.q, scheduleTaskParams{
		Kind:   taskCleanup,
		Reason: "prune settled scheduled tasks older than the retention window",
		After:  taskCleanupEvery,
	}); err != nil {
		return taskRetry(taskExpireRetry, "pruned %d task(s) but could not schedule the next cleanup: %v", n, err)
	}
	return taskDone("pruned %d settled task(s) older than %d days", n, int(taskRetention.Hours()/24))
}

func handleCertRenew(ctx context.Context, r *taskRunner, t db.ScheduledTask) taskOutcome {
	started, skipped := 0, 0
	if r.certs != nil {
		due, err := r.q.ListDomainsDueForRenewal(ctx, certRenewBefore.Seconds())
		if err != nil {
			return taskRetry(taskExpireRetry, "could not list the domains due for renewal: %v", err)
		}
		for _, domain := range due {
			if err := r.certs.Start(domain); err != nil {
				log.Printf("not renewing %s: %v", domain.TLD, err)
				skipped++
				continue
			}
			started++
		}
	}

	if _, err := scheduleTask(ctx, r.q, scheduleTaskParams{
		Kind:   taskCertRenew,
		Reason: "replace certificates that are close to expiring",
		After:  taskCertRenewEvery,
	}); err != nil {
		return taskRetry(taskExpireRetry, "started %d renewal(s) but could not schedule the next sweep: %v", started, err)
	}
	if skipped > 0 {
		return taskDone("started %d certificate renewal(s), skipped %d already in progress", started, skipped)
	}
	return taskDone("started %d certificate renewal(s)", started)
}
