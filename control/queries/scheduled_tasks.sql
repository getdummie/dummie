-- name: CreateScheduledTask :one
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
INSERT INTO scheduled_tasks (kind, reason, run_at)
SELECT sqlc.arg(kind), sqlc.arg(reason),
       now() + make_interval(secs => sqlc.arg(delay_seconds)::double precision)
WHERE NOT EXISTS (
    SELECT 1 FROM scheduled_tasks
    WHERE kind = sqlc.arg(kind) AND status IN ('pending', 'running')
);

-- name: ClaimDueScheduledTasks :many
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
UPDATE scheduled_tasks
SET status      = sqlc.arg(status),
    detail      = sqlc.arg(detail),
    locked_by   = '',
    locked_at   = NULL,
    finished_at = now(),
    updated_at  = now()
WHERE id = sqlc.arg(id);

-- name: RetryScheduledTask :exec
UPDATE scheduled_tasks
SET status     = 'pending',
    detail     = sqlc.arg(detail),
    run_at     = now() + make_interval(secs => sqlc.arg(delay_seconds)::double precision),
    locked_by  = '',
    locked_at  = NULL,
    updated_at = now()
WHERE id = sqlc.arg(id);

-- name: ReclaimStaleScheduledTasks :execrows
UPDATE scheduled_tasks
SET status     = 'pending',
    detail     = 'the control server restarted while this task was running, so it was queued again',
    locked_by  = '',
    locked_at  = NULL,
    updated_at = now()
WHERE status = 'running'
  AND locked_at < now() - make_interval(secs => sqlc.arg(lease_seconds)::double precision);

-- name: CancelLiveScheduledTasksForSubject :exec
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
UPDATE scheduled_tasks
SET status      = 'cancelled',
    detail      = sqlc.arg(detail),
    finished_at = now(),
    updated_at  = now()
WHERE id = sqlc.arg(id) AND status = 'pending'
RETURNING *;

-- name: RunScheduledTaskNow :one
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
SELECT * FROM scheduled_tasks
WHERE subject_kind = sqlc.arg(subject_kind)
  AND subject_id = ANY(sqlc.arg(subjects)::uuid[])
  AND status IN ('pending', 'running')
ORDER BY run_at;

-- name: CountOverdueScheduledTasks :one
SELECT count(*) FROM scheduled_tasks
WHERE status = 'pending'
  AND run_at < now() - make_interval(secs => sqlc.arg(grace_seconds)::double precision);

-- name: DeleteSettledScheduledTasksBefore :execrows
DELETE FROM scheduled_tasks
WHERE status IN ('done', 'failed', 'cancelled')
  AND finished_at < now() - make_interval(secs => sqlc.arg(age_seconds)::double precision);
