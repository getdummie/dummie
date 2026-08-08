-- name: UpsertAgentByMachineID :one
-- UpsertAgentByMachineID re-enrolls an existing machine in place rather than
-- creating a duplicate row: the token is rotated and the facts refreshed. A
-- previously revoked agent is restored, because holding a valid enrollment key
-- is the authorization to enroll.
INSERT INTO agents (machine_id, hostname, token_hash, os, os_version, arch, agent_version, enrolled_key_id, domain_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (machine_id) DO UPDATE
SET hostname        = EXCLUDED.hostname,
    token_hash      = EXCLUDED.token_hash,
    os              = EXCLUDED.os,
    os_version      = EXCLUDED.os_version,
    arch            = EXCLUDED.arch,
    agent_version   = EXCLUDED.agent_version,
    enrolled_key_id = EXCLUDED.enrolled_key_id,
    -- COALESCE, not EXCLUDED: a domain already on the row was either assigned
    -- deliberately or picked at first enrollment, and a re-enroll is not a
    -- statement about which domain the machine belongs to.
    domain_id       = COALESCE(agents.domain_id, EXCLUDED.domain_id),
    revoked         = false,
    updated_at      = now()
RETURNING *;

-- name: GetAgentByMachineID :one
SELECT * FROM agents WHERE machine_id = $1;

-- name: GetAgentByTokenHash :one
SELECT * FROM agents
WHERE token_hash = $1
LIMIT 1;

-- name: GetAgentByID :one
SELECT * FROM agents
WHERE id = $1;

-- name: CountAgents :one
SELECT count(*) FROM agents;

-- name: ListAgents :many
-- The domain is joined in rather than resolved per row by the caller: the admin
-- table shows the TLD, not the id, and one join beats a lookup per agent.
SELECT sqlc.embed(agents), d.tld AS domain_tld
FROM agents
LEFT JOIN domains d ON d.id = agents.domain_id
ORDER BY agents.created_at DESC
LIMIT $1 OFFSET $2;

-- name: SetAgentOnline :exec
UPDATE agents
SET status = 'online', last_seen_at = now(), last_ip = $2, updated_at = now()
WHERE id = $1;

-- name: SetAgentOffline :exec
UPDATE agents
SET status = 'offline', last_seen_at = now(), updated_at = now()
WHERE id = $1;

-- name: TouchAgentLastSeen :exec
UPDATE agents
SET last_seen_at = now()
WHERE id = $1;

-- name: UpdateAgentFacts :exec
UPDATE agents
SET hostname = $2, os = $3, os_version = $4, arch = $5, agent_version = $6, updated_at = now()
WHERE id = $1;

-- name: UpdateAgentMetrics :exec
-- UpdateAgentMetrics overwrites the host snapshot in place. last_seen_at moves
-- too: a metrics frame is proof the agent is alive, and it arrives often enough
-- that the throttled touch in the read loop rarely has anything left to do.
UPDATE agents
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

-- name: RevokeAgent :exec
UPDATE agents
SET revoked = true, status = 'offline', updated_at = now()
WHERE id = $1;

-- name: DeleteAgent :exec
DELETE FROM agents
WHERE id = $1;

-- name: SetAllAgentsOffline :exec
-- SetAllAgentsOffline runs at startup: the hub is in-memory, so any 'online'
-- row left behind by a previous process is stale.
UPDATE agents
SET status = 'offline', updated_at = now()
WHERE status <> 'offline';

-- name: ListAvailableHosts :many
-- ListAvailableHosts is the host list a self-service caller picks from, least
-- busy first so the default choice spreads load instead of piling onto whichever
-- agent happens to sort first.
--
-- Deliberately NOT filtered on status: that column is a cached copy of
-- connectivity and goes stale -- startup sets every agent 'offline', and a
-- reconnect that has not written back yet would hide a host that is genuinely
-- there. The hub is the authority, and the caller filters on it. Matching what
-- the admin screen does, which reads connectivity only from the hub.
SELECT id, hostname FROM agents
WHERE revoked = false
ORDER BY (
    SELECT count(*) FROM vms
    WHERE vms.agent_id = agents.id AND vms.status IN ('pending', 'running')
) ASC, last_seen_at DESC NULLS LAST;
