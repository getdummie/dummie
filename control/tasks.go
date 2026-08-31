package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"control/internal/db"
)

const (
	taskVMTargetExpire = "vm_target.expire"

	taskVMExpire = "vm.expire"

	taskCleanup = "tasks.cleanup"

	taskCertRenew = "cert.renew"

	taskCustomDomainIssue = "custom_domain.issue"

	taskOSImageBuild = "osimage.build"
)

const (
	subjectVM       = "vm"
	subjectVMTarget = "vm_network_target"

	subjectCustomDomain = "vm_custom_domain"

	subjectOSImage = "osimage"
)

const (
	taskTick = time.Second

	// Raised from four when os image builds arrived: a build holds a worker for
	// as long as the pull takes, and with four slots two of them would stall
	// certificate renewals and vm expiry behind them.
	taskWorkers = 8

	taskTimeout = 30 * time.Second

	// An os image build is a registry pull and a multi-gigabyte upload; it is
	// nothing like the second-scale tasks the defaults were written for.
	taskOSImageBuildTimeout = 30 * time.Minute

	taskLease = 2 * time.Minute

	// The lease has to outlast the timeout, or the reclaim sweep would queue a
	// second copy of a build that is still running.
	taskLongLease = taskOSImageBuildTimeout + 2*time.Minute

	taskReclaimEvery = 30 * time.Second

	taskOverdueGrace = 60 * time.Second

	taskRetention = 30 * 24 * time.Hour
)

const (
	minTaskTTL = 10 * time.Second
	maxTaskTTL = 30 * 24 * time.Hour
)

func parseTTLSeconds(secs int64) (time.Duration, error) {
	if secs == 0 {
		return 0, nil
	}
	ttl := time.Duration(secs) * time.Second
	if secs < 0 || ttl < minTaskTTL {
		return 0, fmt.Errorf("a ttl must be at least %d seconds", int(minTaskTTL.Seconds()))
	}
	if ttl > maxTaskTTL {
		return 0, fmt.Errorf("a ttl must be at most %d days", int(maxTaskTTL.Hours()/24))
	}
	return ttl, nil
}

func formatTTL(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0 && d >= 24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d%time.Hour == 0 && d >= time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d%time.Minute == 0 && d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}

type taskOutcome struct {
	status string
	after time.Duration
	detail string
}

func taskDone(format string, args ...any) taskOutcome {
	return taskOutcome{status: "done", detail: fmt.Sprintf(format, args...)}
}

func taskFailed(format string, args ...any) taskOutcome {
	return taskOutcome{status: "failed", detail: fmt.Sprintf(format, args...)}
}

func taskCancelled(format string, args ...any) taskOutcome {
	return taskOutcome{status: "cancelled", detail: fmt.Sprintf(format, args...)}
}

func taskRetry(after time.Duration, format string, args ...any) taskOutcome {
	return taskOutcome{after: after, detail: fmt.Sprintf(format, args...)}
}

// taskKindTimeouts holds the kinds that need longer than taskTimeout. Every kind
// listed here is also leased for taskLongLease by the reclaim sweep.
var taskKindTimeouts = map[string]time.Duration{
	taskOSImageBuild: taskOSImageBuildTimeout,
}

func taskKindTimeout(kind string) time.Duration {
	if d, ok := taskKindTimeouts[kind]; ok {
		return d
	}
	return taskTimeout
}

func taskLongKinds() []string {
	kinds := make([]string, 0, len(taskKindTimeouts))
	for k := range taskKindTimeouts {
		kinds = append(kinds, k)
	}
	return kinds
}

type taskHandler func(ctx context.Context, r *taskRunner, t db.ScheduledTask) taskOutcome

type taskRunner struct {
	q   *db.Queries
	hub *Hub
	certs *certIssuer
	blobs *blobStore

	instance string

	handlers map[string]taskHandler

	inflight atomic.Int64

	lastTick atomic.Int64
}

func newTaskRunner(q *db.Queries, hub *Hub, certs *certIssuer, blobs *blobStore) *taskRunner {
	r := &taskRunner{
		q:        q,
		hub:      hub,
		certs:    certs,
		blobs:    blobs,
		instance: uuid.NewString(),
	}
	r.handlers = map[string]taskHandler{
		taskVMTargetExpire: handleVMTargetExpire,
		taskVMExpire:       handleVMExpire,
		taskCleanup:        handleTaskCleanup,
		taskCertRenew:      handleCertRenew,
		taskCustomDomainIssue: handleCustomDomainIssue,
		taskOSImageBuild:      handleOSImageBuild,
	}
	return r
}

func (r *taskRunner) run(ctx context.Context) {
	r.reclaim(ctx, 0, 0)
	if err := r.q.CreateSingletonScheduledTask(ctx, db.CreateSingletonScheduledTaskParams{
		Kind:         taskCleanup,
		Reason:       "prune settled scheduled tasks older than the retention window",
		DelaySeconds: taskCleanupEvery.Seconds(),
	}); err != nil {
		log.Printf("could not schedule the task cleanup: %v", err)
	}
	if err := r.q.CreateSingletonScheduledTask(ctx, db.CreateSingletonScheduledTaskParams{
		Kind:         taskCertRenew,
		Reason:       "replace certificates that are close to expiring",
		DelaySeconds: taskCertRenewEvery.Seconds(),
	}); err != nil {
		log.Printf("could not schedule the certificate renewal sweep: %v", err)
	}

	tick := time.NewTicker(taskTick)
	defer tick.Stop()
	reclaim := time.NewTicker(taskReclaimEvery)
	defer reclaim.Stop()

	log.Printf("scheduled task runner started (instance %s, %s tick)", r.instance, taskTick)
	for {
		select {
		case <-ctx.Done():
			log.Print("scheduled task runner stopping")
			return
		case <-reclaim.C:
			r.reclaim(ctx, taskLease, taskLongLease)
		case <-tick.C:
			r.poll(ctx)
		}
	}
}

func (r *taskRunner) poll(ctx context.Context) {
	r.lastTick.Store(time.Now().UnixMilli())

	free := taskWorkers - int(r.inflight.Load())
	if free <= 0 {
		return
	}
	tasks, err := r.q.ClaimDueScheduledTasks(ctx, db.ClaimDueScheduledTasksParams{
		LockedBy: r.instance,
		MaxTasks: int32(free),
	})
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			log.Printf("could not claim scheduled tasks: %v", err)
		}
		return
	}
	for _, t := range tasks {
		r.inflight.Add(1)
		go func(t db.ScheduledTask) {
			defer r.inflight.Add(-1)
			r.execute(ctx, t)
		}(t)
	}
}

func (r *taskRunner) execute(parent context.Context, t db.ScheduledTask) {
	id := uuid.UUID(t.ID.Bytes).String()

	handler, ok := r.handlers[t.Kind]
	if !ok {
		r.settle(parent, t, taskFailed("no handler is registered for the task kind %q", t.Kind))
		return
	}

	ctx, cancel := context.WithTimeout(parent, taskKindTimeout(t.Kind))
	defer cancel()

	out := func() (out taskOutcome) {
		defer func() {
			if p := recover(); p != nil {
				log.Printf("task %s (%s) panicked: %v", id, t.Kind, p)
				out = taskRetry(taskBackoff(t.Attempts), "the task panicked: %v", p)
			}
		}()
		return handler(ctx, r, t)
	}()

	r.settle(parent, t, out)
}

func (r *taskRunner) settle(ctx context.Context, t db.ScheduledTask, out taskOutcome) {
	id := uuid.UUID(t.ID.Bytes).String()

	if out.status == "" {
		if t.Attempts >= t.MaxAttempts {
			out = taskFailed("gave up after %d attempts; the last one said: %s", t.Attempts, out.detail)
		} else {
			after := out.after
			if after <= 0 {
				after = taskBackoff(t.Attempts)
			}
			if err := r.q.RetryScheduledTask(ctx, db.RetryScheduledTaskParams{
				ID:           t.ID,
				Detail:       out.detail,
				DelaySeconds: after.Seconds(),
			}); err != nil {
				log.Printf("could not requeue task %s (%s): %v", id, t.Kind, err)
			}
			return
		}
	}

	if err := r.q.FinishScheduledTask(ctx, db.FinishScheduledTaskParams{
		ID:     t.ID,
		Status: out.status,
		Detail: out.detail,
	}); err != nil {
		log.Printf("could not settle task %s (%s) as %s: %v", id, t.Kind, out.status, err)
		return
	}
	if out.status == "failed" {
		log.Printf("task %s (%s) failed: %s", id, t.Kind, out.detail)
	}
}

func taskBackoff(attempts int32) time.Duration {
	d := time.Duration(1<<min(attempts, 6)) * time.Second
	return min(d, 2*time.Minute)
}

func (r *taskRunner) reclaim(ctx context.Context, lease, longLease time.Duration) {
	n, err := r.q.ReclaimStaleScheduledTasks(ctx, db.ReclaimStaleScheduledTasksParams{
		LongKinds:        taskLongKinds(),
		LongLeaseSeconds: longLease.Seconds(),
		LeaseSeconds:     lease.Seconds(),
	})
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			log.Printf("could not reclaim stale scheduled tasks: %v", err)
		}
		return
	}
	if n > 0 {
		log.Printf("requeued %d scheduled task(s) left running by a previous process", n)
	}
}

func (r *taskRunner) overdue(ctx context.Context) (int64, error) {
	return r.q.CountOverdueScheduledTasks(ctx, taskOverdueGrace.Seconds())
}

func (r *taskRunner) lastTickAt() time.Time {
	ms := r.lastTick.Load()
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

func scheduleTask(ctx context.Context, q *db.Queries, p scheduleTaskParams) (db.ScheduledTask, error) {
	raw := []byte("{}")
	if p.Payload != nil {
		b, err := json.Marshal(p.Payload)
		if err != nil {
			return db.ScheduledTask{}, fmt.Errorf("could not encode the task payload: %w", err)
		}
		raw = b
	}
	maxAttempts := p.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = 20
	}
	return q.CreateScheduledTask(ctx, db.CreateScheduledTaskParams{
		Kind:         p.Kind,
		SubjectKind:  p.SubjectKind,
		SubjectID:    p.SubjectID,
		Payload:      raw,
		Reason:       p.Reason,
		CreatedBy:    p.CreatedBy,
		MaxAttempts:  maxAttempts,
		DelaySeconds: p.After.Seconds(),
	})
}

func inTx(ctx context.Context, pool *pgxpool.Pool, q *db.Queries, fn func(*db.Queries) error) error {
	if pool == nil {
		return errors.New("no database connection is configured")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type scheduleTaskParams struct {
	Kind        string
	SubjectKind string
	SubjectID   pgtype.UUID
	Payload     any
	Reason      string
	CreatedBy   pgtype.UUID
	After time.Duration
	MaxAttempts int32
}

func cancelTasksForSubject(ctx context.Context, q *db.Queries, subjectKind string, subjectID pgtype.UUID, detail string) {
	if err := q.CancelLiveScheduledTasksForSubject(ctx, db.CancelLiveScheduledTasksForSubjectParams{
		SubjectKind: subjectKind,
		SubjectID:   subjectID,
		Detail:      detail,
	}); err != nil {
		log.Printf("could not cancel the scheduled tasks for %s %s: %v",
			subjectKind, uuid.UUID(subjectID.Bytes).String(), err)
	}
}

func liveTaskDeadlines(ctx context.Context, q *db.Queries, subjectKind string, ids []pgtype.UUID) map[string]time.Time {
	out := make(map[string]time.Time, len(ids))
	if len(ids) == 0 {
		return out
	}
	rows, err := q.ListLiveScheduledTasksForSubjects(ctx, db.ListLiveScheduledTasksForSubjectsParams{
		SubjectKind: subjectKind,
		Subjects:    ids,
	})
	if err != nil {
		log.Printf("could not read the scheduled deadlines for %s rows: %v", subjectKind, err)
		return out
	}
	for _, t := range rows {
		if !t.SubjectID.Valid {
			continue
		}
		id := uuid.UUID(t.SubjectID.Bytes).String()
		if _, seen := out[id]; !seen {
			out[id] = t.RunAt.Time
		}
	}
	return out
}
