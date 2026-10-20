-- name: ListGitLabInstances :many
SELECT * FROM gitlab_instances ORDER BY name;

-- name: GetGitLabInstance :one
SELECT * FROM gitlab_instances WHERE id = $1;

-- name: GetGitLabInstanceByName :one
SELECT * FROM gitlab_instances WHERE name = $1;

-- name: CreateGitLabInstance :one
INSERT INTO gitlab_instances (name, base_url, flavor, updated_by)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: UpdateGitLabInstance :one
UPDATE gitlab_instances
SET base_url = $2, updated_by = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteGitLabInstance :execrows
DELETE FROM gitlab_instances WHERE id = $1;

-- name: CreateGitLabIntegration :one
INSERT INTO integrations (owner_id, name, kind, readonly, all_repos, gitlab_instance_id, token_enc, gitlab_username)
VALUES ($1, $2, 'gitlab', $3, true, $4, $5, $6)
RETURNING *;

-- name: UpdateGitLabIntegrationToken :execrows
UPDATE integrations
SET token_enc = $3, gitlab_username = $4, updated_at = now()
WHERE id = $1 AND owner_id = $2 AND kind = 'gitlab';

-- name: ListGitLabGrantsForVM :many
-- The broker's authorization query for gitlab, with the same load-bearing
-- predicate as ListIntegrationGrantsForVM: client_id comes from the calling
-- host's own token, never from the request.
SELECT i.id            AS integration_id,
       i.name          AS integration_name,
       i.owner_id,
       i.readonly,
       i.token_enc,
       g.base_url,
       v.name          AS vm_name
FROM vms v
JOIN vm_integrations vi ON vi.vm_id = v.id
JOIN integrations i ON i.id = vi.integration_id AND i.kind = 'gitlab'
JOIN gitlab_instances g ON g.id = i.gitlab_instance_id
WHERE v.client_id = sqlc.arg(client_id)
  AND v.ip = sqlc.arg(vm_ip)
  AND g.name = sqlc.arg(instance_name)
  AND i.owner_id = v.created_by
ORDER BY i.readonly, i.created_at;
