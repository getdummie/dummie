-- 0020_scheduled_tasks: work the control plane owes the future.
--
-- One row per action that should happen at a moment nobody is waiting for: an
-- allowance that expires, a sandbox that outlives its TTL. The control server
-- runs these itself -- dclient holds no timers, because a host with its own
-- expiry clock would be a second opinion about policy in the one place that
-- cannot be audited from here.
--
-- The table is both the queue and the audit log. A finished row is not deleted
-- on completion, so "what did this control plane decide to do, and did it" is
-- answerable after the fact; tasks.cleanup prunes the tail on its own schedule.
CREATE TABLE scheduled_tasks (
    id UUID NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Which handler runs this, e.g. 'vm.expire'. Deliberately NOT a CHECK
    -- constraint: the registry in tasks.go is the list, and requiring a migration
    -- to add a kind is the one thing that would stop this being a generic
    -- mechanism. A kind with no handler fails loudly at claim time instead.
    kind TEXT NOT NULL,

    -- What the task acts on. Split into columns rather than left in payload
    -- because it is looked up by, not just read: the VM page asks "is anything
    -- scheduled against this row" on every render, and that has to be an index
    -- hit rather than a jsonb scan.
    --
    -- No foreign key on purpose. The audit row has to outlive its subject -- the
    -- whole point of a vm.expire record is that it is still there once the VM is
    -- gone -- so referential integrity here would delete exactly the evidence
    -- somebody came looking for.
    subject_kind TEXT NOT NULL DEFAULT '',
    subject_id   UUID,

    -- Everything else the handler needs, plus a snapshot of whatever the audit
    -- view must still be able to name once the subject is deleted (the VM's name,
    -- the destination that was allowed).
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,

    status TEXT NOT NULL DEFAULT 'pending'
           CHECK (status IN ('pending', 'running', 'done', 'failed', 'cancelled')),

    -- When the task becomes due. Compared against now() in the database, never
    -- against a Go clock: the poller and the row have to agree about the time,
    -- and only one of them is authoritative.
    run_at TIMESTAMPTZ NOT NULL,

    -- attempts counts claims, not failures: a task claimed and then lost to a
    -- crash has still had a go, and a poison task that wedges the process every
    -- time must not be retried forever.
    attempts     INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 20,

    -- The last thing that happened, in a sentence: the error while a task is
    -- still retrying, or why it ended once it stops. One column rather than
    -- separate error/resolution fields -- every consumer wants "what is the
    -- latest word on this row", and two columns would make that a branch.
    detail TEXT NOT NULL DEFAULT '',

    -- Why the task exists at all, written when it is scheduled. A queue nobody
    -- can explain is one nobody can safely drain, the same argument as the note
    -- on vm_network_targets.
    reason TEXT NOT NULL DEFAULT '',

    -- Whoever asked for the thing that scheduled this. NULL for a task the
    -- control plane schedules for itself, like the cleanup.
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,

    -- The lease. A claimed row carries the id of the process holding it, so a
    -- task left 'running' by a crash or a rebuild is distinguishable from one
    -- genuinely in flight and can be reclaimed once the lease goes stale.
    locked_by TEXT NOT NULL DEFAULT '',
    locked_at TIMESTAMPTZ,

    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ
);

-- The poller's index. Partial, so the every-second "is anything due" query scans
-- only the rows that could be -- not the finished history, which is most of the
-- table after a week.
CREATE INDEX idx_scheduled_tasks_due
    ON scheduled_tasks (run_at) WHERE status = 'pending';

-- The lookup that replaces an expires_at column on vms and vm_network_targets.
-- The deadline lives here and nowhere else, so this index is the only thing
-- making "when does this VM expire" cheap to answer.
CREATE INDEX idx_scheduled_tasks_subject
    ON scheduled_tasks (subject_kind, subject_id);

CREATE INDEX idx_scheduled_tasks_status ON scheduled_tasks (status);

-- Two live expiries for one subject is a bug, not a second intention: they would
-- both fire, and the second would act on something already gone. Enforced here
-- rather than by a check in the handler, because the race is between two
-- requests and only the database sees both.
CREATE UNIQUE INDEX idx_scheduled_tasks_live_subject
    ON scheduled_tasks (kind, subject_kind, subject_id)
    WHERE status IN ('pending', 'running') AND subject_id IS NOT NULL;

-- Reclaim a stale lease by finding it: 'running' rows are few, but this is run
-- on a timer forever and a seq scan over the whole history for each sweep is
-- avoidable.
CREATE INDEX idx_scheduled_tasks_leases
    ON scheduled_tasks (locked_at) WHERE status = 'running';
