-- name: CreateScheduledTask :one
-- CreateScheduledTask records work due later. run_at is computed in the database
-- from a delay rather than passed as a timestamp: the caller's clock has no say
-- in when a task is due, and a server a few seconds off would otherwise write
-- deadlines nobody can explain.
INSERT INTO scheduled_tasks (
    kind, subject_kind, subject_id, payload, reason, created_by, max_attempts, run_at
)
VALUES (
    sqlc.arg(kind), sqlc.arg(subject_kind), sqlc.arg(subject_id), sqlc.arg(payload),
    sqlc.arg(reason), sqlc.arg(created_by), sqlc.arg(max_attempts),
    now() + make_interval(secs => sqlc.arg(delay_seconds)::double precision)
)
RETURNING *;

-- name: CreateSingletonScheduledTask :exec
-- CreateSingletonScheduledTask schedules a task of a kind that must never have
-- more than one instance pending -- the housekeeping ones, which act on the fleet
-- rather than on a subject and so cannot be deduplicated by the live-subject
-- index. Run at startup, so it must be a no-op when the row is already there.
INSERT INTO scheduled_tasks (kind, reason, run_at)
SELECT sqlc.arg(kind), sqlc.arg(reason),
       now() + make_interval(secs => sqlc.arg(delay_seconds)::double precision)
WHERE NOT EXISTS (
    SELECT 1 FROM scheduled_tasks
    WHERE kind = sqlc.arg(kind) AND status IN ('pending', 'running')
);

-- name: ClaimDueScheduledTasks :many
-- ClaimDueScheduledTasks takes ownership of up to `limit` due tasks in one
-- statement: the rows are marked 'running' and stamped with this process's
-- lease, so nothing else can pick them up.
--
-- FOR UPDATE SKIP LOCKED is what makes the claim safe rather than the current
-- process count. There is one control server today, but the hub is already
-- in-memory and per-process, so the day there are two the queue must not be the
-- thing that breaks -- and skipping locked rows costs nothing while there is one.
--
-- attempts is incremented on claim, not on failure. A task the process dies
-- holding has still been attempted, and counting only clean failures would let a
-- handler that reliably crashes be retried forever.
UPDATE scheduled_tasks
SET status    = 'running',
    attempts  = attempts + 1,
    locked_by = sqlc.arg(locked_by),
    locked_at = now(),
    updated_at = now()
WHERE id IN (
    SELECT id FROM scheduled_tasks
    WHERE status = 'pending' AND run_at <= now()
    ORDER BY run_at
    LIMIT sqlc.arg(max_tasks)
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: FinishScheduledTask :exec
-- FinishScheduledTask settles a task that will not run again. The lease is
-- cleared so a reclaim sweep cannot resurrect it.
UPDATE scheduled_tasks
SET status      = sqlc.arg(status),
    detail      = sqlc.arg(detail),
    locked_by   = '',
    locked_at   = NULL,
    finished_at = now(),
    updated_at  = now()
WHERE id = sqlc.arg(id);

-- name: RetryScheduledTask :exec
-- RetryScheduledTask puts a claimed task back in the queue. Used both for an
-- expected wait (the host is offline) and for an unexpected failure, because the
-- handler contract is the same either way: converge one step, or say why not yet.
UPDATE scheduled_tasks
SET status     = 'pending',
    detail     = sqlc.arg(detail),
    run_at     = now() + make_interval(secs => sqlc.arg(delay_seconds)::double precision),
    locked_by  = '',
    locked_at  = NULL,
    updated_at = now()
WHERE id = sqlc.arg(id);

-- name: ReclaimStaleScheduledTasks :execrows
-- ReclaimStaleScheduledTasks is crash recovery. A 'running' row whose lease has
-- gone stale belonged to a process that is no longer around to finish it -- the
-- air rebuild in development, a restart in production -- so it goes back in the
-- queue.
--
-- This is why every handler has to be idempotent: a task reclaimed this way may
-- already have done part or all of its work.
UPDATE scheduled_tasks
SET status     = 'pending',
    detail     = 'the control server restarted while this task was running, so it was queued again',
    locked_by  = '',
    locked_at  = NULL,
    updated_at = now()
WHERE status = 'running'
  AND locked_at < now() - make_interval(secs => sqlc.arg(lease_seconds)::double precision);

-- name: CancelLiveScheduledTasksForSubject :exec
-- CancelLiveScheduledTasksForSubject withdraws work that no longer makes sense
-- because the thing it was going to act on is already gone -- a VM destroyed
-- before its TTL, a temporary destination removed by hand.
--
-- The task would cancel itself on its next run anyway, since every handler
-- checks its subject first. Doing it here is about the audit view: a queue full
-- of pending expiries for VMs that no longer exist is one an operator learns to
-- ignore.
UPDATE scheduled_tasks
SET status      = 'cancelled',
    detail      = sqlc.arg(detail),
    locked_by   = '',
    locked_at   = NULL,
    finished_at = now(),
    updated_at  = now()
WHERE subject_kind = sqlc.arg(subject_kind)
  AND subject_id = sqlc.arg(subject_id)
  AND status IN ('pending', 'running');

-- name: CancelScheduledTask :one
-- CancelScheduledTask is the admin action. Scoped to 'pending' rather than also
-- 'running': a task mid-flight has already started acting, and reporting it
-- cancelled would claim something that did not happen. No rows means it was
-- already settled, which the handler reports as a conflict.
UPDATE scheduled_tasks
SET status      = 'cancelled',
    detail      = sqlc.arg(detail),
    finished_at = now(),
    updated_at  = now()
WHERE id = sqlc.arg(id) AND status = 'pending'
RETURNING *;

-- name: RunScheduledTaskNow :one
-- RunScheduledTaskNow is the debugging escape hatch: bring a task forward, and
-- give a failed one another go.
--
-- attempts is reset, because otherwise re-running a task that exhausted its
-- budget fails again immediately -- which is the opposite of what the operator
-- clicking it is asking for. The count so far is not lost: it is in the detail
-- of why it failed, and the row's updated_at moved.
UPDATE scheduled_tasks
SET status     = 'pending',
    run_at     = now(),
    attempts   = 0,
    detail     = sqlc.arg(detail),
    locked_by  = '',
    locked_at  = NULL,
    finished_at = NULL,
    updated_at = now()
WHERE id = sqlc.arg(id) AND status IN ('pending', 'failed')
RETURNING *;

-- name: GetScheduledTask :one
SELECT * FROM scheduled_tasks WHERE id = $1;

-- name: ListScheduledTasks :many
-- ListScheduledTasks is the admin view. Both filters are optional in one query
-- rather than a query per combination: an empty status list means every status,
-- an empty kind means every kind.
--
-- The ordering puts unfinished work first, soonest due at the top -- that is the
-- half an operator is watching -- and history after it, newest first. Written as
-- one ORDER BY rather than two queries because a page that mixes them still has
-- to be stably ordered.
SELECT * FROM scheduled_tasks
WHERE (cardinality(sqlc.arg(statuses)::text[]) = 0 OR status = ANY(sqlc.arg(statuses)::text[]))
  AND (sqlc.arg(kind)::text = '' OR kind = sqlc.arg(kind)::text)
ORDER BY (status IN ('pending', 'running')) DESC,
         CASE WHEN status IN ('pending', 'running') THEN run_at END ASC,
         updated_at DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: CountScheduledTasks :one
SELECT count(*) FROM scheduled_tasks
WHERE (cardinality(sqlc.arg(statuses)::text[]) = 0 OR status = ANY(sqlc.arg(statuses)::text[]))
  AND (sqlc.arg(kind)::text = '' OR kind = sqlc.arg(kind)::text);

-- name: ListLiveScheduledTasksForSubjects :many
-- ListLiveScheduledTasksForSubjects is how a deadline reaches the UI. There is no
-- expires_at column on vms or vm_network_targets -- the task row is the only place
-- the deadline is written -- so a list of VMs resolves its countdowns with one
-- call to this rather than a column read.
--
-- Batched over ids for that reason: per-row it would be a query per VM on every
-- render of the list.
SELECT * FROM scheduled_tasks
WHERE subject_kind = sqlc.arg(subject_kind)
  AND subject_id = ANY(sqlc.arg(subjects)::uuid[])
  AND status IN ('pending', 'running')
ORDER BY run_at;

-- name: CountOverdueScheduledTasks :one
-- CountOverdueScheduledTasks is the health signal. A due task that is still
-- pending means the poller is not running, or is behind -- which for this table
-- is not a backlog but an allowance that outlived what the user was promised.
SELECT count(*) FROM scheduled_tasks
WHERE status = 'pending'
  AND run_at < now() - make_interval(secs => sqlc.arg(grace_seconds)::double precision);

-- name: DeleteSettledScheduledTasksBefore :execrows
-- DeleteSettledScheduledTasksBefore prunes the audit tail. Only settled rows, and
-- only by age of settlement: a task still pending is not history no matter when
-- it was created.
DELETE FROM scheduled_tasks
WHERE status IN ('done', 'failed', 'cancelled')
  AND finished_at < now() - make_interval(secs => sqlc.arg(age_seconds)::double precision);
