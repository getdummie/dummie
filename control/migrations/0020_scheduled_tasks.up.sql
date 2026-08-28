CREATE TABLE scheduled_tasks (
    id UUID NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),

    kind TEXT NOT NULL,

    subject_kind TEXT NOT NULL DEFAULT '',
    subject_id   UUID,

    payload JSONB NOT NULL DEFAULT '{}'::jsonb,

    status TEXT NOT NULL DEFAULT 'pending'
           CHECK (status IN ('pending', 'running', 'done', 'failed', 'cancelled')),

    run_at TIMESTAMPTZ NOT NULL,

    attempts     INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 20,

    detail TEXT NOT NULL DEFAULT '',

    reason TEXT NOT NULL DEFAULT '',

    created_by UUID REFERENCES users(id) ON DELETE SET NULL,

    locked_by TEXT NOT NULL DEFAULT '',
    locked_at TIMESTAMPTZ,

    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ
);

CREATE INDEX idx_scheduled_tasks_due
    ON scheduled_tasks (run_at) WHERE status = 'pending';

CREATE INDEX idx_scheduled_tasks_subject
    ON scheduled_tasks (subject_kind, subject_id);

CREATE INDEX idx_scheduled_tasks_status ON scheduled_tasks (status);

CREATE UNIQUE INDEX idx_scheduled_tasks_live_subject
    ON scheduled_tasks (kind, subject_kind, subject_id)
    WHERE status IN ('pending', 'running') AND subject_id IS NOT NULL;

CREATE INDEX idx_scheduled_tasks_leases
    ON scheduled_tasks (locked_at) WHERE status = 'running';
