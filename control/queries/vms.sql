-- name: CreateVM :one
-- CreateVM records the intent before the job is pushed to the agent. The row id
-- doubles as the job's correlation id, which is what lets the result frame find
-- its way back to exactly this row.
INSERT INTO vms (agent_id, name, boot, cpus, memory_mib, disk_mib, spec, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: MarkVMRunning :exec
-- MarkVMRunning applies what the agent actually built: the id, name and address
-- are allocated on the host, so they are only known once it answers.
UPDATE vms
SET status     = 'running',
    vm_id      = $2,
    name       = $3,
    boot       = $4,
    cpus       = $5,
    memory_mib = $6,
    ip         = $7,
    last_error = '',
    started_at = now(),
    updated_at = now()
WHERE id = $1;

-- name: MarkVMFailed :exec
UPDATE vms
SET status = 'failed', last_error = $2, updated_at = now()
WHERE id = $1;

-- name: UpsertVMFromInventory :exec
-- UpsertVMFromInventory records what an agent reports it is actually running.
-- This is how a VM created locally with `dagent vm create` gets adopted: the
-- control plane learns about it the same way it learns about one it asked for.
--
-- created_at comes from the host, not from now(): the VM's age is a fact about
-- the guest, not about when this server first heard of it. started_at is kept if
-- we already knew it, since the host does not report when a boot happened.
INSERT INTO vms (agent_id, vm_id, name, status, boot, cpus, memory_mib, ip, created_at, started_at, reported_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now())
ON CONFLICT (agent_id, vm_id) WHERE vm_id <> '' DO UPDATE
SET name        = EXCLUDED.name,
    status      = EXCLUDED.status,
    boot        = EXCLUDED.boot,
    cpus        = EXCLUDED.cpus,
    memory_mib  = EXCLUDED.memory_mib,
    ip          = EXCLUDED.ip,
    started_at  = COALESCE(vms.started_at, EXCLUDED.started_at),
    reported_at = now(),
    updated_at  = now();

-- name: MarkMissingVMsGone :exec
-- MarkMissingVMsGone settles the other half of an inventory report: a row the
-- agent no longer lists has been removed on the host.
--
-- Only rows the host had already assigned an id to are considered. A 'pending'
-- row has no id yet and is waiting on its result frame, and a 'failed' one never
-- got that far -- neither is missing, and neither should be touched here.
UPDATE vms
SET status = 'gone', updated_at = now()
WHERE agent_id = $1
  AND vm_id <> ''
  AND status IN ('running', 'stopped')
  AND NOT (vm_id = ANY(sqlc.arg(vm_ids)::text[]));

-- name: DeleteAdoptedVM :exec
-- DeleteAdoptedVM resolves the one race between the two ways a row is born: an
-- inventory report can land after the agent has written vm.json but before its
-- result frame arrives, adopting a VM that already has a pending row waiting for
-- it. The adopted duplicate is dropped so the pending row -- which holds the
-- spec that was asked for -- is the one that survives.
DELETE FROM vms
WHERE agent_id = $1 AND vm_id = $2 AND id <> $3;

-- name: SetVMStatus :exec
-- SetVMStatus settles a start, stop or destroy as soon as the agent confirms it,
-- rather than waiting up to a full inventory tick for the row to catch up.
--
-- reported_at moves too. A result frame *is* the host vouching for this VM, at
-- this moment, on the same socket an inventory report would use -- so treating
-- it as older than it is would show a VM that was just confirmed as stale, which
-- is both wrong and alarming. A host that dies immediately afterwards is still
-- caught, by the ordinary staleness window.
UPDATE vms
SET status = $2, last_error = '', reported_at = now(), updated_at = now()
WHERE id = $1;

-- name: SetVMLastError :exec
-- SetVMLastError records a failed action without changing the status. A stop
-- that failed most likely leaves the VM running, so claiming otherwise would be
-- worse than saying nothing.
UPDATE vms
SET last_error = $2, updated_at = now()
WHERE id = $1;

-- name: GetVM :one
SELECT * FROM vms
WHERE id = $1;

-- name: CountVMs :one
SELECT count(*) FROM vms;

-- name: ListVMs :many
SELECT * FROM vms
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountVMsByAgent :one
SELECT count(*) FROM vms
WHERE agent_id = $1;

-- name: ListVMsByAgent :many
SELECT * FROM vms
WHERE agent_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountVMsByOwner :one
SELECT count(*) FROM vms
WHERE created_by = $1;

-- name: ListVMsByOwner :many
SELECT * FROM vms
WHERE created_by = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: GetVMForOwner :one
-- GetVMForOwner is the ownership check and the read in one statement. Doing it
-- as two -- read, then compare created_by in Go -- is the same query written so
-- that forgetting the second half silently leaks another user's VM.
SELECT * FROM vms
WHERE id = $1 AND created_by = $2;

-- name: SumActiveVMUsageByOwner :one
-- SumActiveVMUsageByOwner totals what a user is currently holding, for the
-- quota check on create.
--
-- 'failed' and 'gone' are excluded: neither has anything running on a host, so
-- counting them would let a run of failed creates permanently consume someone's
-- allowance. 'pending' IS counted -- it is a create in flight, and leaving it
-- out lets concurrent requests each see room that only one of them can have.
SELECT COALESCE(SUM(cpus), 0)::int AS cpus,
       COALESCE(SUM(memory_mib), 0)::int AS memory_mib,
       COALESCE(SUM(disk_mib), 0)::int AS disk_mib
FROM vms
WHERE created_by = $1
  AND status IN ('pending', 'running', 'stopped');

-- name: DeleteVM :exec
DELETE FROM vms
WHERE id = $1;

-- name: FailPendingVMsForAgent :exec
-- FailPendingVMsForAgent runs when an agent's socket drops and at startup: a
-- pending row is waiting on a result frame that can no longer arrive, so it
-- would otherwise sit there forever claiming to be in progress.
UPDATE vms
SET status = 'failed', last_error = $2, updated_at = now()
WHERE agent_id = $1 AND status = 'pending';

-- name: FailAllPendingVMs :exec
-- FailAllPendingVMs is the startup counterpart: in-flight jobs belonged to the
-- previous process's sockets and cannot be resumed.
UPDATE vms
SET status = 'failed', last_error = $1, updated_at = now()
WHERE status = 'pending';
