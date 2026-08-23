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

// The task handlers. Each one reads its subject, converges one step and says
// what happened; the runner owns the row. Two rules they all follow:
//
//   - Idempotent. A reclaimed lease re-runs an attempt that may already have
//     applied its work, so "already true" has to be success rather than an error.
//   - A subject that is gone is cancelled, not failed. An allowance somebody
//     removed by hand and a VM destroyed before its TTL are the mechanism working,
//     and marking those failed would train an operator to ignore the failed count.

// taskExpireRetry is how long an expiry waits when it cannot act yet -- almost
// always because the host is not connected. A fixed interval rather than
// exponential backoff: what it is waiting for is a machine coming back, and
// backing off would only make it later than it has to be.
const taskExpireRetry = 30 * time.Second

// taskExpireAttempts is the budget for a VM expiry. Deliberately far above the
// default: at taskExpireRetry that is two hours of a host being unreachable
// before the control plane gives up and asks for a human, and a host down for
// twenty minutes is an ordinary morning.
const taskExpireAttempts = 240

// taskCleanupEvery is how often the housekeeping task runs. It reschedules
// itself, so this is both the initial delay and the interval.
const taskCleanupEvery = 24 * time.Hour

// taskCertRenewEvery is how often the renewal sweep runs, and like the cleanup
// it is both the initial delay and the interval. Daily against a thirty-day
// window, so a certificate has a month of chances before anyone notices.
const taskCertRenewEvery = 24 * time.Hour

// vmTargetExpirePayload is what an allowance expiry carries beyond its subject
// id: enough to name what was withdrawn once the row it points at is deleted.
// Without it the audit view of a completed expiry reads "some destination".
type vmTargetExpirePayload struct {
	VMID        string `json:"vm_id"`
	VMName      string `json:"vm_name"`
	Destination string `json:"destination"`
	TTLSeconds  int64  `json:"ttl_seconds"`
}

// vmExpirePayload is the same idea for a VM: its name is what an operator
// recognises, and after the destroy the row may be pruned.
type vmExpirePayload struct {
	VMName     string `json:"vm_name"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

// handleVMTargetExpire withdraws one temporarily allowed destination.
//
// Deleting the row is the whole of the change -- the ruleset and the Corefile are
// compiled from the table, so regenerating them for the host is what makes the
// withdrawal real. Both are pushed for the same reason the interactive delete
// pushes both: the Corefile stops answering the name and the ruleset stops
// passing the traffic, and whichever lands second finishes the job.
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

	// Regenerated even when the delete found nothing: a previous attempt that
	// deleted the row and then failed to write its outcome is exactly the case the
	// lease reclaims, and it is the push that may not have happened.
	pushSuricataRules(ctx, r.q, r.hub, row.ClientID)
	pushCoreDNSConfig(ctx, r.q, r.hub, row.ClientID)

	if n == 0 {
		return taskDone("the destination %s was already gone; the host's policy was regenerated anyway", row.Destination)
	}
	return taskDone("withdrew %s from %s", row.Destination, row.VMName)
}

// handleVMExpire destroys a VM whose TTL has run out.
//
// A convergence loop rather than a single push, because a destroy is a frame the
// host answers later: the attempt that sends it does not learn the outcome, and
// the row going to 'gone' is what settles this task. Re-sending is harmless --
// dclient treats every VM action as idempotent -- so the loop is simply "is it
// gone yet, and if not ask again".
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
		// Either this task's own destroy landed, or the owner got there first. Both
		// are the outcome it wanted.
		return taskDone("%s is gone", vm.Name)
	case "failed":
		// The create never produced a guest, so there is nothing on any host to
		// destroy and no TTL left to enforce.
		return taskCancelled("%s never started, so it had nothing to destroy", vm.Name)
	case "pending":
		// The TTL is measured from when the VM was asked for, so a slow create can
		// still be in flight when it fires. Waiting is the only correct move: there is
		// no id to destroy yet, and the create is about to produce one.
		return taskRetry(taskExpireRetry, "the create is still in flight, so there is nothing to destroy yet")
	}
	if vm.VMID == "" {
		return taskRetry(taskExpireRetry, "the host has not reported an id for this vm yet")
	}

	clientID := uuid.UUID(vm.ClientID.Bytes).String()
	if !r.hub.Connected(clientID) {
		// The expiry is late from here on, and knowingly so: dclient holds no timers,
		// so nothing on the host will do this in our absence. Said plainly in the
		// detail because that sentence is what an operator reads in the admin view.
		return taskRetry(taskExpireRetry, "the host running %s is not connected, so the destroy is waiting", vm.Name)
	}

	// The envelope id is the vms row id, the same correlation every other VM
	// action uses -- so the result frame settles the row through the ordinary
	// path in client.go, including the policy regeneration a destroy triggers.
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

	// Not done: the host has been asked, not yet answered. The next attempt reads
	// the row and finishes when it says 'gone'.
	return taskRetry(taskExpireRetry, "asked the host to destroy %s; waiting for it to confirm", vm.Name)
}

// handleTaskCleanup prunes settled tasks and puts itself back in the queue.
//
// Rescheduling from inside the handler is what makes a recurring task possible
// without a second mechanism: there is no cron table, just a task whose last act
// is to schedule the next one.
func handleTaskCleanup(ctx context.Context, r *taskRunner, t db.ScheduledTask) taskOutcome {
	n, err := r.q.DeleteSettledScheduledTasksBefore(ctx, taskRetention.Seconds())
	if err != nil {
		return taskRetry(taskExpireRetry, "could not prune settled tasks: %v", err)
	}
	// Scheduled before reporting done, so a failure to schedule the next one leaves
	// this task retrying rather than leaving the fleet with no cleanup at all.
	if _, err := scheduleTask(ctx, r.q, scheduleTaskParams{
		Kind:   taskCleanup,
		Reason: "prune settled scheduled tasks older than the retention window",
		After:  taskCleanupEvery,
	}); err != nil {
		return taskRetry(taskExpireRetry, "pruned %d task(s) but could not schedule the next cleanup: %v", n, err)
	}
	return taskDone("pruned %d settled task(s) older than %d days", n, int(taskRetention.Hours()/24))
}

// handleCertRenew starts an order for every automated domain whose certificate
// is running out, and puts itself back in the queue.
//
// It starts orders rather than running them. An attempt here is given thirty
// seconds and a dns-01 order takes minutes, so the handler's job is to decide
// what needs doing; the issuer does it in the background and records the outcome
// on the domain row, which is where the admin screen reads it from.
//
// Only the automated mode is listed -- an uploaded or hand-walked certificate
// cannot be replaced without a person, and putting one here would produce a
// failure every single day instead of an expiry warning on the screen.
func handleCertRenew(ctx context.Context, r *taskRunner, t db.ScheduledTask) taskOutcome {
	started, skipped := 0, 0
	if r.certs != nil {
		due, err := r.q.ListDomainsDueForRenewal(ctx, certRenewBefore.Seconds())
		if err != nil {
			return taskRetry(taskExpireRetry, "could not list the domains due for renewal: %v", err)
		}
		for _, domain := range due {
			// Start refuses when an order is already running for the domain, which is
			// the whole of the guard against a sweep piling onto yesterday's attempt.
			if err := r.certs.Start(domain); err != nil {
				log.Printf("not renewing %s: %v", domain.TLD, err)
				skipped++
				continue
			}
			started++
		}
	}

	// Scheduled before reporting done, for the same reason the cleanup is: a
	// failure to queue the next sweep should leave this one retrying rather than
	// leave the fleet with nothing watching its expiry dates.
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
