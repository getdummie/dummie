-- name: UpsertAgentByMachineID :one
-- UpsertAgentByMachineID re-enrolls an existing machine in place rather than
-- creating a duplicate row: the token is rotated and the facts refreshed. A
-- previously revoked agent is restored, because holding a valid enrollment key
-- is the authorization to enroll.
INSERT INTO agents (machine_id, hostname, token_hash, os, os_version, arch, agent_version, enrolled_key_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (machine_id) DO UPDATE
SET hostname        = EXCLUDED.hostname,
    token_hash      = EXCLUDED.token_hash,
    os              = EXCLUDED.os,
    os_version      = EXCLUDED.os_version,
    arch            = EXCLUDED.arch,
    agent_version   = EXCLUDED.agent_version,
    enrolled_key_id = EXCLUDED.enrolled_key_id,
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
SELECT * FROM agents
ORDER BY created_at DESC
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
