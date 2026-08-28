-- name: UpsertClientByMachineID :one
INSERT INTO clients (machine_id, hostname, token_hash, os, os_version, arch, client_version, enrolled_key_id, domain_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (machine_id) DO UPDATE
SET hostname        = EXCLUDED.hostname,
    token_hash      = EXCLUDED.token_hash,
    os              = EXCLUDED.os,
    os_version      = EXCLUDED.os_version,
    arch            = EXCLUDED.arch,
    client_version   = EXCLUDED.client_version,
    enrolled_key_id = EXCLUDED.enrolled_key_id,
    domain_id       = COALESCE(clients.domain_id, EXCLUDED.domain_id),
    revoked         = false,
    updated_at      = now()
RETURNING *;

-- name: GetClientByMachineID :one
SELECT * FROM clients WHERE machine_id = $1;

-- name: GetClientByTokenHash :one
SELECT * FROM clients
WHERE token_hash = $1
LIMIT 1;

-- name: GetClientByID :one
SELECT * FROM clients
WHERE id = $1;

-- name: CountClients :one
SELECT count(*) FROM clients;

-- name: ListClients :many
SELECT sqlc.embed(clients), d.tld AS domain_tld
FROM clients
LEFT JOIN domains d ON d.id = clients.domain_id
ORDER BY clients.created_at DESC
LIMIT $1 OFFSET $2;

-- name: SetClientOnline :exec
UPDATE clients
SET status = 'online', last_seen_at = now(), last_ip = $2, updated_at = now()
WHERE id = $1;

-- name: SetClientOffline :exec
UPDATE clients
SET status = 'offline', last_seen_at = now(), updated_at = now()
WHERE id = $1;

-- name: TouchClientLastSeen :exec
UPDATE clients
SET last_seen_at = now()
WHERE id = $1;

-- name: UpdateClientFacts :exec
UPDATE clients
SET hostname = $2, os = $3, os_version = $4, arch = $5, client_version = $6, updated_at = now()
WHERE id = $1;

-- name: UpdateClientMetrics :exec
UPDATE clients
SET cpu_count        = $2,
    cpu_percent      = $3,
    load1            = $4,
    load5            = $5,
    load15           = $6,
    mem_total_bytes  = $7,
    mem_used_bytes   = $8,
    disk_total_bytes = $9,
    disk_used_bytes  = $10,
    uptime_seconds   = $11,
    metrics_at       = now(),
    last_seen_at     = now()
WHERE id = $1;

-- name: RevokeClient :exec
UPDATE clients
SET revoked = true, status = 'offline', updated_at = now()
WHERE id = $1;

-- name: DeleteClient :exec
DELETE FROM clients
WHERE id = $1;

-- name: SetAllClientsOffline :exec
UPDATE clients
SET status = 'offline', updated_at = now()
WHERE status <> 'offline';

-- name: ListAvailableHosts :many
SELECT id, hostname FROM clients
WHERE revoked = false
ORDER BY (
    SELECT count(*) FROM vms
    WHERE vms.client_id = clients.id AND vms.status IN ('pending', 'running')
) ASC, last_seen_at DESC NULLS LAST;

-- name: SetClientDomain :one
UPDATE clients
SET domain_id = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateClientInstalledVersions :exec
UPDATE clients
SET dpipe_installed_version = $2,
    proxy_installed_version = $3,
    updated_at              = now()
WHERE id = $1;

-- name: UpdateClientServiceVersions :one
UPDATE clients
SET dclient_version      = $2,
    dclient_download_url = $3,
    dpipe_version        = $4,
    dpipe_download_url   = $5,
    proxy_version        = $6,
    proxy_download_url   = $7,
    updated_at           = now()
WHERE id = $1
RETURNING *;
