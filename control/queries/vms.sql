-- name: CreateVM :one
INSERT INTO vms (client_id, name, boot, cpus, memory_mib, disk_mib, spec, created_by, default_port, public_ports)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: MarkVMRunning :exec
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
INSERT INTO vms (client_id, vm_id, name, status, boot, cpus, memory_mib, ip, created_at, started_at, reported_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now())
ON CONFLICT (client_id, vm_id) WHERE vm_id <> '' DO UPDATE
SET status      = EXCLUDED.status,
    boot        = EXCLUDED.boot,
    cpus        = EXCLUDED.cpus,
    memory_mib  = EXCLUDED.memory_mib,
    ip          = EXCLUDED.ip,
    started_at  = COALESCE(vms.started_at, EXCLUDED.started_at),
    reported_at = now(),
    updated_at  = now();

-- name: MarkMissingVMsGone :exec
UPDATE vms
SET status = 'gone', updated_at = now()
WHERE client_id = $1
  AND vm_id <> ''
  AND status IN ('running', 'stopped')
  AND NOT (vm_id = ANY(sqlc.arg(vm_ids)::text[]));

-- name: DeleteAdoptedVM :exec
DELETE FROM vms
WHERE client_id = $1 AND vm_id = $2 AND id <> $3;

-- name: SetVMStatus :exec
UPDATE vms
SET status = $2, last_error = '', reported_at = now(), updated_at = now()
WHERE id = $1;

-- name: SetVMLastError :exec
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

-- name: CountVMsByClient :one
SELECT count(*) FROM vms
WHERE client_id = $1;

-- name: ListVMsByClient :many
SELECT * FROM vms
WHERE client_id = $1
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
SELECT * FROM vms
WHERE id = $1 AND created_by = $2;

-- name: GetVMForOwnerByHostname :one
SELECT v.id
FROM vms v
JOIN clients a ON a.id = v.client_id
JOIN domains d ON d.id = a.domain_id
WHERE v.name = sqlc.arg(name)
  AND d.tld = sqlc.arg(domain_tld)
  AND v.status <> 'gone'
  AND (sqlc.arg(is_admin)::boolean OR v.created_by = sqlc.arg(owner_id));

-- name: SumActiveVMUsageByOwner :one
SELECT COALESCE(SUM(cpus), 0)::int AS cpus,
       COALESCE(SUM(memory_mib), 0)::int AS memory_mib,
       COALESCE(SUM(disk_mib), 0)::int AS disk_mib
FROM vms
WHERE created_by = $1
  AND status IN ('pending', 'running', 'stopped');

-- name: ListProxySSHUsersByClient :many
SELECT v.ip AS vm_ip, v.vm_id AS host_vm_id, v.name AS vm_name, u.public_key
FROM vms v
JOIN users u ON u.id = v.created_by
WHERE v.client_id = $1
  AND v.ip <> ''
  AND v.status <> 'gone'
  AND u.public_key <> ''
ORDER BY v.created_at, v.vm_id;

-- name: ListProxyRDPUsersByClient :many
SELECT v.id AS vm_pk, v.ip AS vm_ip, v.vm_id AS host_vm_id, v.name AS vm_name, v.rdp_nonce
FROM vms v
WHERE v.client_id = $1
  AND v.ip <> ''
  AND v.status <> 'gone'
ORDER BY v.created_at, v.vm_id;

-- name: ListProxyHTTPRoutesByClient :many
SELECT v.name AS vm_name, v.ip AS vm_ip, v.vm_id AS host_vm_id,
       v.default_port, v.public_ports, d.tld AS domain_tld
FROM vms v
JOIN clients a ON a.id = v.client_id
JOIN domains d ON d.id = a.domain_id
WHERE v.client_id = $1
  AND v.ip <> ''
  AND v.status <> 'gone'
ORDER BY v.created_at, v.vm_id;

-- name: UpdateVMPortsForOwner :one
UPDATE vms
SET default_port = $3,
    public_ports = $4,
    updated_at   = now()
WHERE id = $1 AND created_by = $2
RETURNING *;

-- name: DeleteVM :exec
DELETE FROM vms
WHERE id = $1;

-- name: FailPendingVMsForClient :exec
UPDATE vms
SET status = 'failed', last_error = $2, updated_at = now()
WHERE client_id = $1 AND status = 'pending';

-- name: FailAllPendingVMs :exec
UPDATE vms
SET status = 'failed', last_error = $1, updated_at = now()
WHERE status = 'pending';

-- name: RotateVMRDPNonceForOwner :one
UPDATE vms
SET rdp_nonce  = gen_random_uuid()::text,
    updated_at = now()
WHERE id = $1 AND created_by = $2
RETURNING *;
