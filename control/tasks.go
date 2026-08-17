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

// Background work the control plane owes the future: an allowance that expires,
// a sandbox that outlives its TTL. It runs in this process rather than in a
// worker of its own, because every handler's effect is "change a row, then push
// the regenerated file" -- and the sockets those pushes travel down live in this
// process's hub. A separate worker would have to come back through the API to
// reach them.
//
// The word "task" throughout, never "job": a job in this codebase is a frame
// pushed to dclient (proto.TypeJob), and a task is a row in scheduled_tasks. A
// task may well send a job.

// Task kinds. Adding one is a constant, a handler and a line in the registry --
// no migration, because scheduled_tasks.kind is deliberately unconstrained.
const (
	// taskVMTargetExpire removes one temporary allowed destination. Its subject is
	// the vm_network_targets row.
	taskVMTargetExpire = "vm_target.expire"

	// taskVMExpire destroys a VM whose TTL has run out. Its subject is the vms row.
	taskVMExpire = "vm.expire"

	// taskCleanup prunes settled tasks and reschedules itself. Housekeeping with no
	// subject, which is why it is deduplicated by kind rather than by the
	// live-subject index.
	taskCleanup = "tasks.cleanup"
)

// Subject kinds, matching the table a subject_id points into.
const (
	subjectVM       = "vm"
	subjectVMTarget = "vm_network_target"
)

const (
	// taskTick is how often the poller asks whether anything is due. One second
	// rather than five: the query is an index-only scan of a partial index that
	// returns nothing on almost every tick, so the precision is close to free.
	//
	// It does not make expiry exact -- once a task deletes a row, the effect still
	// has to travel a websocket and a suricata reload -- but it takes the control
	// plane out of that error budget.
	taskTick = time.Second

	// taskWorkers bounds how many tasks run at once. The poller claims at most as
	// many as there are free slots, so a slow handler delays other tasks rather
	// than piling up goroutines.
	taskWorkers = 4

	// taskTimeout is the ceiling on one attempt. A handler that hangs on a database
	// call must give its slot back rather than hold it until the process restarts;
	// the attempt comes round again on the next tick.
	taskTimeout = 30 * time.Second

	// taskLease is how long a claimed task may sit untouched before another poller
	// pass assumes the process holding it is gone. Comfortably longer than
	// taskTimeout so a slow-but-alive attempt is never reclaimed underneath itself.
	taskLease = 2 * time.Minute

	// taskReclaimEvery is the lease sweep. Slower than the tick on purpose: crash
	// recovery is not urgent, and there is no reason to write to the table every
	// second looking for something that happens on restarts.
	taskReclaimEvery = 30 * time.Second

	// taskOverdueGrace is how late a due task may be before it counts as overdue in
	// the health report. Above one tick plus a claim, so ordinary scheduling jitter
	// is not an alarm.
	taskOverdueGrace = 60 * time.Second

	// taskRetention is how long settled tasks stay readable. They are the audit
	// trail of what this control plane decided to do, which is worth more than the
	// rows cost.
	taskRetention = 30 * 24 * time.Hour
)

// Bounds on a TTL anyone can ask for. The floor is low enough for "let this
// through for a moment" to be meaningful and high enough that the deadline is
// not inside the time it takes to apply; the ceiling is there because a TTL
// measured in years is someone asking for a permanent thing through a temporary
// mechanism.
const (
	minTaskTTL = 10 * time.Second
	maxTaskTTL = 30 * 24 * time.Hour
)

// parseTTLSeconds validates a caller-supplied TTL. Zero means "no TTL", which is
// not an error -- it is the ordinary case, and every route that accepts a TTL
// accepts its absence.
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

// formatTTL renders a duration the way the reason line should read: "45s",
// "30m", "2h", "3d". time.Duration's own String gives "2h0m0s", which is not a
// sentence anybody wants to read in an audit view.
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

// --- outcomes ---------------------------------------------------------------

// taskOutcome is what a handler returns. Four shapes, and the runner writes the
// row from it -- a handler never updates its own row, so there is one place that
// decides what a status means.
type taskOutcome struct {
	// status is one of the settled statuses, or "" for a retry.
	status string
	// after is how long to wait before the next attempt. Retries only.
	after time.Duration
	// detail is the sentence that lands in scheduled_tasks.detail: the reason it
	// ended, or what it is waiting for.
	detail string
}

// taskDone: the work is applied, or was already true. Both are success -- a
// handler that finds its subject already gone has nothing left to converge.
func taskDone(format string, args ...any) taskOutcome {
	return taskOutcome{status: "done", detail: fmt.Sprintf(format, args...)}
}

// taskFailed: this will not work on a retry either. Ends up in front of an
// operator, which is the point.
func taskFailed(format string, args ...any) taskOutcome {
	return taskOutcome{status: "failed", detail: fmt.Sprintf(format, args...)}
}

// taskCancelled: the work no longer makes sense, and that is not a failure --
// the target was removed by hand, the VM was destroyed early.
func taskCancelled(format string, args ...any) taskOutcome {
	return taskOutcome{status: "cancelled", detail: fmt.Sprintf(format, args...)}
}

// taskRetry: not yet. The waiting is expected (a host that is offline) as often
// as it is a fault, so both come back this way and the attempt budget is what
// eventually turns a wait into a failure.
func taskRetry(after time.Duration, format string, args ...any) taskOutcome {
	return taskOutcome{after: after, detail: fmt.Sprintf(format, args...)}
}

// taskHandler runs one attempt. The contract is narrow on purpose: read the
// subject, converge one step, say what happened. It must be idempotent -- a
// reclaimed lease re-runs an attempt that may already have done its work -- and
// it must not block for longer than taskTimeout.
type taskHandler func(ctx context.Context, r *taskRunner, t db.ScheduledTask) taskOutcome

// --- runner -----------------------------------------------------------------

type taskRunner struct {
	q   *db.Queries
	hub *Hub

	// instance identifies this process in the lease columns. Regenerated per
	// start, so a task held by a previous run is recognisable as stale even on the
	// same machine.
	instance string

	handlers map[string]taskHandler

	// inflight is how many attempts are running. The poller claims only as many
	// tasks as there are free slots, so this is the whole of the backpressure.
	inflight atomic.Int64

	// lastTick is when the poller last completed a pass, exposed by the
	// healthcheck. A poller that has silently died is otherwise invisible until
	// somebody notices a VM that outlived its TTL.
	lastTick atomic.Int64
}

func newTaskRunner(q *db.Queries, hub *Hub) *taskRunner {
	r := &taskRunner{
		q:        q,
		hub:      hub,
		instance: uuid.NewString(),
	}
	r.handlers = map[string]taskHandler{
		taskVMTargetExpire: handleVMTargetExpire,
		taskVMExpire:       handleVMExpire,
		taskCleanup:        handleTaskCleanup,
	}
	return r
}

// run is the poller. It returns when ctx is done; in-flight attempts are left to
// their own timeouts, and anything they do not finish is reclaimed by whichever
// process comes next.
func (r *taskRunner) run(ctx context.Context) {
	// Immediately, not on the first sweep: at startup every 'running' row is by
	// definition from a process that no longer exists, and waiting half a minute
	// to say so delays every expiry that was in flight over a restart.
	r.reclaim(ctx, 0)
	// Housekeeping schedules itself from here rather than from a migration, so a
	// fleet that has never run the cleanup gets one the next time it starts.
	if err := r.q.CreateSingletonScheduledTask(ctx, db.CreateSingletonScheduledTaskParams{
		Kind:         taskCleanup,
		Reason:       "prune settled scheduled tasks older than the retention window",
		DelaySeconds: taskCleanupEvery.Seconds(),
	}); err != nil {
		log.Printf("could not schedule the task cleanup: %v", err)
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
			r.reclaim(ctx, taskLease)
		case <-tick.C:
			r.poll(ctx)
		}
	}
}

// poll claims what it has room for and dispatches it. Deliberately does not wait
// for the attempts it starts: the claim marked those rows 'running', so the next
// tick cannot pick them up again, and a handler waiting on a slow host must not
// hold up an expiry that is due now.
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
		// Logged rather than fatal, and not once per second: a database that is down
		// comes back, and the queue is durable across it.
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

// execute runs one attempt and writes the row from what it returns. The handler
// itself never touches scheduled_tasks: statuses mean the same thing for every
// kind because exactly one function assigns them.
func (r *taskRunner) execute(parent context.Context, t db.ScheduledTask) {
	id := uuid.UUID(t.ID.Bytes).String()

	handler, ok := r.handlers[t.Kind]
	if !ok {
		// A kind with no handler is a deployment that rolled back past the code that
		// understands it, or a typo at the call site. Neither improves with retries,
		// and both need somebody to look.
		r.settle(parent, t, taskFailed("no handler is registered for the task kind %q", t.Kind))
		return
	}

	ctx, cancel := context.WithTimeout(parent, taskTimeout)
	defer cancel()

	out := func() (out taskOutcome) {
		// A panicking handler must not take the control server with it. The task
		// retries, so a transient panic self-heals and a deterministic one exhausts
		// its attempts and lands in the admin view.
		defer func() {
			if p := recover(); p != nil {
				log.Printf("task %s (%s) panicked: %v", id, t.Kind, p)
				out = taskRetry(taskBackoff(t.Attempts), "the task panicked: %v", p)
			}
		}()
		return handler(ctx, r, t)
	}()

	// The parent context, not ctx: an attempt that ran out of time still has to
	// record that it did, and writing that through the context that just expired
	// would lose it.
	r.settle(parent, t, out)
}

// settle writes the outcome. A retry past the attempt budget becomes a failure
// here rather than in each handler -- the budget is the runner's business, and a
// handler that had to know its own attempt count would be a handler that can get
// it wrong.
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
		// The lease is what saves this: a task whose outcome could not be written
		// stays 'running' and is reclaimed, then re-run. Idempotent handlers are
		// what make that safe rather than a second application of the same work.
		log.Printf("could not settle task %s (%s) as %s: %v", id, t.Kind, out.status, err)
		return
	}
	if out.status == "failed" {
		log.Printf("task %s (%s) failed: %s", id, t.Kind, out.detail)
	}
}

// taskBackoff is the delay for a retry the handler did not put a number on --
// a panic, or an error it had no expectation about. Handlers that are waiting
// for something specific (a host to come back) pass their own interval, because
// they know what they are waiting for and exponential backoff on a host reboot
// would just be slower.
func taskBackoff(attempts int32) time.Duration {
	d := time.Duration(1<<min(attempts, 6)) * time.Second
	return min(d, 2*time.Minute)
}

// reclaim puts leases older than lease back in the queue. lease of 0 takes
// everything currently marked running, which is what startup wants.
func (r *taskRunner) reclaim(ctx context.Context, lease time.Duration) {
	n, err := r.q.ReclaimStaleScheduledTasks(ctx, lease.Seconds())
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

// overdue is the healthcheck's number: tasks that are due, past the grace
// window, and still waiting. Anything above zero for more than a moment means
// the promise attached to a TTL is not being kept.
func (r *taskRunner) overdue(ctx context.Context) (int64, error) {
	return r.q.CountOverdueScheduledTasks(ctx, taskOverdueGrace.Seconds())
}

// lastTickAt is when the poller last ran, or the zero time if it never has.
func (r *taskRunner) lastTickAt() time.Time {
	ms := r.lastTick.Load()
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// --- scheduling helpers -----------------------------------------------------

// scheduleTask is the one way a task is created. Every caller goes through it so
// the delay is expressed in one unit and the payload is marshalled in one place.
//
// It takes a *db.Queries rather than reading the runner's, so a caller inside a
// transaction can schedule the task and write the thing it acts on together --
// which is the point: a VM with a TTL whose expiry row failed to insert is a
// sandbox that never expires, and nothing would ever notice.
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

// inTx runs fn against a transactional Queries. It exists for one shape: the row
// a TTL applies to and the task that enforces it have to be written together.
// Split across two statements, a create whose second insert fails is a sandbox
// that never expires -- and since the deadline lives only in the task row, there
// would be nothing left to notice it by.
func inTx(ctx context.Context, pool *pgxpool.Pool, q *db.Queries, fn func(*db.Queries) error) error {
	if pool == nil {
		return errors.New("no database connection is configured")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	// Safe after a successful Commit: rolling back a finished transaction is a
	// no-op, and this way no early return can leave one open.
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
	// After is how far in the future the task is due, measured by the database
	// from the moment of the insert.
	After time.Duration
	// MaxAttempts overrides the default budget. Worth setting for a task whose
	// retry interval is long -- the default times a 30s wait is only ten minutes,
	// which is shorter than a host reboot.
	MaxAttempts int32
}

// cancelTasksForSubject withdraws the pending work against something that has
// just gone away by another route. Best-effort: the handler would cancel itself
// on its next run anyway, so a failure here costs a tidy audit view rather than
// correctness.
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

// liveTaskDeadlines maps subject id -> when its soonest live task is due, for
// the countdowns in the UI. One query for a whole page of rows.
//
// An error is logged and swallowed: this decorates a list that is worth showing
// without it, and a failed lookup here should not turn a VM list into a 500.
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
	// Ordered by run_at, so the first one seen for a subject is the soonest.
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
