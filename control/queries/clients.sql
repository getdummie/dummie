-- name: UpsertClientByMachineID :one
-- UpsertClientByMachineID re-enrolls an existing machine in place rather than
-- creating a duplicate row: the token is rotated and the facts refreshed. A
-- previously revoked client is restored, because holding a valid enrollment key
-- is the authorization to enroll.
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
    -- COALESCE, not EXCLUDED: a domain already on the row was either assigned
    -- deliberately or picked at first enrollment, and a re-enroll is not a
    -- statement about which domain the machine belongs to.
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
-- The domain is joined in rather than resolved per row by the caller: the admin
-- table shows the TLD, not the id, and one join beats a lookup per client.
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
-- UpdateClientMetrics overwrites the host snapshot in place. last_seen_at moves
-- too: a metrics frame is proof the client is alive, and it arrives often enough
-- that the throttled touch in the read loop rarely has anything left to do.
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
-- SetAllClientsOffline runs at startup: the hub is in-memory, so any 'online'
-- row left behind by a previous process is stale.
UPDATE clients
SET status = 'offline', updated_at = now()
WHERE status <> 'offline';

-- name: ListAvailableHosts :many
-- ListAvailableHosts is the host list a self-service caller picks from, least
-- busy first so the default choice spreads load instead of piling onto whichever
-- client happens to sort first.
--
-- Deliberately NOT filtered on status: that column is a cached copy of
-- connectivity and goes stale -- startup sets every client 'offline', and a
-- reconnect that has not written back yet would hide a host that is genuinely
-- there. The hub is the authority, and the caller filters on it. Matching what
-- the admin screen does, which reads connectivity only from the hub.
SELECT id, hostname FROM clients
WHERE revoked = false
ORDER BY (
    SELECT count(*) FROM vms
    WHERE vms.client_id = clients.id AND vms.status IN ('pending', 'running')
) ASC, last_seen_at DESC NULLS LAST;

-- name: SetClientDomain :one
-- SetClientDomain moves one client to a domain, or clears it when the argument
-- is null.
--
-- Enrollment assigns a domain only when there is exactly one to assign, and only
-- on the row's first insert, so without this an installation that added its
-- domain after its hosts enrolled has no way to attach them -- and a host with no
-- domain publishes no vms and can never be served over tls.
UPDATE clients
SET domain_id = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateClientInstalledVersions :exec
-- UpdateClientInstalledVersions records what dpipe and dproxy actually are on the
-- host, as the host itself reported them.
--
-- Written through rather than merged: an empty value means the host could not say,
-- which is a fact about the host now and not a reason to keep showing what it said
-- last time. :exec because nothing waits on the row -- it is a report arriving,
-- not a change being made.
UPDATE clients
SET dpipe_installed_version = $2,
    proxy_installed_version = $3,
    updated_at              = now()
WHERE id = $1;

-- name: UpdateClientServiceVersions :one
-- UpdateClientServiceVersions sets which build of dclient, dpipe and dproxy this
-- host should run. All six together rather than one at a time: an operator moving
-- a host onto a release moves all three, and a partial write would leave the host
-- running a mixture nobody asked for.
--
-- An empty version means "track this control server's version"; an empty url
-- means "build it from the version".
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
