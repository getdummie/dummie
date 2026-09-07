-- name: UpsertGitHubApp :one
INSERT INTO github_apps (app_id, slug, name, private_key)
VALUES ($1, $2, $3, $4)
ON CONFLICT (app_id) DO UPDATE
SET slug        = EXCLUDED.slug,
    name        = EXCLUDED.name,
    private_key = COALESCE(NULLIF(EXCLUDED.private_key, ''), github_apps.private_key),
    updated_at  = now()
RETURNING *;

-- name: GetGitHubApp :one
-- Most recently saved wins: correcting a mistyped app id inserts a new row
-- rather than updating the old one, and the correction has to be the one used.
SELECT * FROM github_apps
ORDER BY updated_at DESC
LIMIT 1;

-- name: DeleteOtherGitHubApps :exec
-- One app per control plane. Replacing it leaves the old row unreachable, and
-- its installations along with it, so they go too.
DELETE FROM github_apps
WHERE id <> $1;

-- name: DeleteGitHubApp :exec
DELETE FROM github_apps
WHERE id = $1;

-- name: UpsertGitHubInstallation :one
INSERT INTO github_installations (
    app_pk, installation_id, account_login, account_type, repository_selection, installed_by
)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (app_pk, installation_id) DO UPDATE
SET account_login        = EXCLUDED.account_login,
    account_type         = EXCLUDED.account_type,
    repository_selection = EXCLUDED.repository_selection,
    suspended            = false,
    last_error           = '',
    updated_at           = now()
RETURNING *;

-- name: ListGitHubInstallationsByOwner :many
SELECT gi.* FROM github_installations gi
WHERE gi.installed_by = $1
ORDER BY gi.account_login;

-- name: GetGitHubInstallation :one
SELECT * FROM github_installations
WHERE id = $1;

-- name: SetInstallationSuspended :exec
UPDATE github_installations
SET suspended  = $2,
    last_error = $3,
    updated_at = now()
WHERE id = $1;

-- name: CreateIntegration :one
INSERT INTO integrations (owner_id, name, kind, readonly)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListIntegrationsByOwner :many
SELECT i.*,
       gi.account_login,
       gi.suspended,
       gi.repository_selection,
       (SELECT count(*) FROM integration_repos r WHERE r.integration_id = i.id) AS repo_count,
       (SELECT count(*) FROM vm_integrations v WHERE v.integration_id = i.id)   AS vm_count
FROM integrations i
LEFT JOIN github_installations gi ON gi.id = i.installation_pk
WHERE i.owner_id = $1
ORDER BY i.name;

-- name: GetIntegrationForOwner :one
SELECT i.*,
       gi.account_login,
       gi.suspended,
       gi.repository_selection,
       gi.installation_id
FROM integrations i
LEFT JOIN github_installations gi ON gi.id = i.installation_pk
WHERE i.id = $1 AND i.owner_id = $2;

-- name: GetIntegrationByNameForOwner :one
SELECT * FROM integrations
WHERE owner_id = $1 AND name = $2;

-- name: UpdateIntegrationFlags :one
UPDATE integrations
SET all_repos  = $2,
    readonly   = $3,
    updated_at = now()
WHERE id = $1 AND owner_id = $4
RETURNING *;

-- name: SetIntegrationInstallation :exec
UPDATE integrations
SET installation_pk = $2,
    updated_at      = now()
WHERE id = $1;

-- name: DeleteIntegrationForOwner :exec
DELETE FROM integrations
WHERE id = $1 AND owner_id = $2;

-- name: ListIntegrationRepos :many
SELECT repo_owner, repo_name FROM integration_repos
WHERE integration_id = $1
ORDER BY repo_owner, repo_name;

-- name: DeleteIntegrationRepos :exec
DELETE FROM integration_repos
WHERE integration_id = $1;

-- name: InsertIntegrationRepo :exec
INSERT INTO integration_repos (integration_id, repo_owner, repo_name)
VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;

-- name: AttachIntegrationToVM :exec
INSERT INTO vm_integrations (vm_id, integration_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: DetachIntegrationFromVM :exec
DELETE FROM vm_integrations
WHERE vm_id = $1 AND integration_id = $2;

-- name: ListIntegrationVMs :many
SELECT v.id, v.name, v.ip, v.client_id
FROM vm_integrations vi
JOIN vms v ON v.id = vi.vm_id
WHERE vi.integration_id = $1
ORDER BY v.name;

-- name: ListVMIntegrationsForOwner :many
SELECT i.id, i.name, i.readonly, i.all_repos
FROM vm_integrations vi
JOIN integrations i ON i.id = vi.integration_id
WHERE vi.vm_id = $1 AND i.owner_id = $2
ORDER BY i.name;

-- name: ListIntegrationGrantsForVM :many
-- The broker's authorization query. v.client_id = $1 is the load-bearing
-- predicate: the client id comes from the calling host's own authenticated
-- token, never from the request, so a host cannot mint a token for a vm that
-- lives somewhere else.
SELECT i.id            AS integration_id,
       i.name          AS integration_name,
       i.all_repos,
       i.readonly,
       gi.id           AS installation_pk,
       gi.installation_id,
       gi.account_login,
       gi.suspended,
       ga.app_id,
       ga.private_key,
       v.id            AS vm_pk,
       v.name          AS vm_name
FROM vms v
JOIN vm_integrations vi ON vi.vm_id = v.id
JOIN integrations i ON i.id = vi.integration_id AND i.kind = sqlc.arg(kind)
JOIN github_installations gi ON gi.id = i.installation_pk
JOIN github_apps ga ON ga.id = gi.app_pk
WHERE v.client_id = sqlc.arg(client_id)
  AND v.ip = sqlc.arg(vm_ip)
ORDER BY i.name;

-- name: VMIntegrationAllowsRepo :one
-- An attachment with no scoping rows inherits the integration's whole list;
-- one with them is limited to exactly those.
SELECT (
    NOT EXISTS (
        SELECT 1 FROM vm_integration_repos scope
        WHERE scope.vm_id = sqlc.arg(vm_id)
          AND scope.integration_id = sqlc.arg(integration_id)
    )
    OR EXISTS (
        SELECT 1 FROM vm_integration_repos hit
        WHERE hit.vm_id = sqlc.arg(vm_id)
          AND hit.integration_id = sqlc.arg(integration_id)
          AND lower(hit.repo_owner) = lower(sqlc.arg(repo_owner)::text)
          AND lower(hit.repo_name) = lower(sqlc.arg(repo_name)::text)
    )
)::boolean;

-- name: ListVMIntegrationRepos :many
SELECT repo_owner, repo_name FROM vm_integration_repos
WHERE vm_id = $1 AND integration_id = $2
ORDER BY repo_owner, repo_name;

-- name: DeleteVMIntegrationRepos :exec
DELETE FROM vm_integration_repos
WHERE vm_id = $1 AND integration_id = $2;

-- name: InsertVMIntegrationRepo :exec
INSERT INTO vm_integration_repos (vm_id, integration_id, repo_owner, repo_name)
VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING;

-- name: CountVMIntegrationRepos :one
SELECT count(*) FROM vm_integration_repos
WHERE vm_id = $1 AND integration_id = $2;

-- name: FindIntegrationRepo :one
-- Case-insensitive, because github is: codingcoffee/x and codingCoffee/x are
-- the same repository. Returns the stored spelling so the minted token names
-- the repository the way github does.
SELECT repo_owner, repo_name FROM integration_repos
WHERE integration_id = sqlc.arg(integration_id)
  AND lower(repo_owner) = lower(sqlc.arg(repo_owner)::text)
  AND lower(repo_name) = lower(sqlc.arg(repo_name)::text);

-- name: ListIntegrationReposForVM :many
SELECT DISTINCT r.repo_owner, r.repo_name
FROM vm_integrations vi
JOIN integration_repos r ON r.integration_id = vi.integration_id
WHERE vi.vm_id = $1
ORDER BY r.repo_owner, r.repo_name;

-- name: CreateGitHubInstallState :exec
INSERT INTO github_install_states (state_hash, owner_id, integration_id)
VALUES ($1, $2, $3);

-- name: TakeGitHubInstallState :one
-- Single use, and expired rows are never returned: a state is only good for the
-- redirect it was minted for.
DELETE FROM github_install_states
WHERE state_hash = $1
  AND created_at > now() - make_interval(secs => sqlc.arg(within_seconds)::float)
RETURNING owner_id, integration_id;

-- name: DeleteExpiredGitHubInstallStates :exec
DELETE FROM github_install_states
WHERE created_at < now() - make_interval(secs => sqlc.arg(within_seconds)::float);

-- name: ListClientIDsWithIntegrations :many
SELECT DISTINCT v.client_id
FROM vm_integrations vi
JOIN vms v ON v.id = vi.vm_id
WHERE vi.integration_id = $1;
