-- name: CreateVM :one
-- CreateVM records the intent before the job is pushed to the agent. The row id
-- doubles as the job's correlation id, which is what lets the result frame find
-- its way back to exactly this row.
INSERT INTO vms (agent_id, name, boot, cpus, memory_mib, disk_mib, spec, created_by, default_port, public_ports)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: MarkVMRunning :exec
-- MarkVMRunning applies what the agent actually built: the id and the address
-- are allocated on the host, so they are only known once it answers.
--
-- The name is NOT taken from the host. It was chosen here, it is unique across
-- the fleet, and it is what proxy routes http by -- so accepting the host's copy
-- of it would let a rename on one machine either collide with another VM or move
-- a live route out from under whoever is using it.
UPDATE vms
SET status     = 'running',
    vm_id      = $2,
    boot       = $3,
    cpus       = $4,
    memory_mib = $5,
    ip         = $6,
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
--
-- name is insert-only: it is absent from the update list below because it is
-- what the VM's http route is keyed on, so re-taking it from the host on every
-- inventory tick would move that route underneath whoever is using it -- and
-- could collide with a name another VM already holds.
INSERT INTO vms (agent_id, vm_id, name, status, boot, cpus, memory_mib, ip, created_at, started_at, reported_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now())
ON CONFLICT (agent_id, vm_id) WHERE vm_id <> '' DO UPDATE
SET status      = EXCLUDED.status,
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

-- name: GetVMForOwnerByHostname :one
-- GetVMForOwnerByHostname resolves the name proxy routes on -- a VM's name under
-- the domain of the agent running it -- back to the VM, and checks in the same
-- statement that the caller may reach it. Used by the /login hand-off, which is
-- handed a hostname and nothing else.
--
-- The ownership test is in the WHERE for the same reason it is in GetVMForOwner:
-- written as a read followed by a comparison in Go, it is a check a later edit
-- can drop without the query stopping working.
--
-- is_admin widens it to any VM rather than being a second query, so there is one
-- statement that decides who may be handed a token for a host. An unowned VM
-- (created on the host, created_by NULL) is reachable only by an admin -- there
-- is no user to match, and treating "nobody owns it" as "everybody owns it"
-- would publish every adopted guest to every account.
--
-- 'gone' rows are excluded: the name may since have been re-used, and a token
-- for a hostname that no longer routes anywhere is not worth minting.
SELECT v.id
FROM vms v
JOIN agents a ON a.id = v.agent_id
JOIN domains d ON d.id = a.domain_id
WHERE v.name = sqlc.arg(name)
  AND d.tld = sqlc.arg(domain_tld)
  AND v.status <> 'gone'
  AND (sqlc.arg(is_admin)::boolean OR v.created_by = sqlc.arg(owner_id));

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

-- name: ListProxySSHUsersByAgent :many
-- ListProxySSHUsersByAgent is the input to the proxy.yaml generator: every VM on
-- the host that can be reached over ssh, carrying the key of whoever owns it.
--
-- One query for the whole host, like the Suricata one, because the file is
-- written as a whole -- a VM missed here is a VM its owner silently loses access
-- to until the next regeneration.
--
-- An inner join on users drops VMs with no owner: one adopted from a host's
-- inventory report was created outside the control plane, so there is no key to
-- route with. A user with no key on file drops out for the same reason.
--
-- Rows without an address are skipped -- there is nothing to point the route at
-- -- and 'gone' VMs are excluded because their address goes back to the pool and
-- will be handed to some other guest, which a stale route would then expose to
-- the wrong user's key.
SELECT v.ip AS vm_ip, v.vm_id AS host_vm_id, v.name AS vm_name, u.public_key
FROM vms v
JOIN users u ON u.id = v.created_by
WHERE v.agent_id = $1
  AND v.ip <> ''
  AND v.status <> 'gone'
  AND u.public_key <> ''
ORDER BY v.created_at, v.vm_id;

-- name: ListProxyHTTPRoutesByAgent :many
-- ListProxyHTTPRoutesByAgent is the other half of the proxy.yaml input: every VM
-- on the host that can be reached over http, with the ports it publishes and the
-- domain its hostname sits under.
--
-- The join on domains is inner, so a host whose agent has no domain contributes
-- nothing. That is the honest outcome rather than a gap: the hostname is the VM
-- name under that domain, so without one there is no name to route on, and
-- inventing a suffix would publish a hostname the operator never configured and
-- nothing resolves.
--
-- Unlike the ssh side this does not join users: an http route exposes a port the
-- guest chose to listen on, so it does not depend on who owns the VM or on their
-- having a key. Rows with no address are still skipped, and 'gone' VMs excluded,
-- for the same reason -- an address that has gone back to the pool will be handed
-- to another guest, and a stale route would then publish that one under this
-- name.
SELECT v.name AS vm_name, v.ip AS vm_ip, v.vm_id AS host_vm_id,
       v.default_port, v.public_ports, d.tld AS domain_tld
FROM vms v
JOIN agents a ON a.id = v.agent_id
JOIN domains d ON d.id = a.domain_id
WHERE v.agent_id = $1
  AND v.ip <> ''
  AND v.status <> 'gone'
ORDER BY v.created_at, v.vm_id;

-- name: UpdateVMPortsForOwner :one
-- UpdateVMPortsForOwner changes what a VM publishes. Scoped to the owner in the
-- statement, like GetVMForOwner: an ownership check written as a separate read
-- is one a later edit can drop without the query stopping working.
--
-- Only the ports. The name is settled at create and the address is the host's,
-- so this is the whole of what an owner may change about how their VM is routed.
UPDATE vms
SET default_port = $3,
    public_ports = $4,
    updated_at   = now()
WHERE id = $1 AND created_by = $2
RETURNING *;

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
