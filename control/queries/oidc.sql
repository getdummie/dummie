-- name: ListOIDCProviders :many
SELECT * FROM oidc_providers
ORDER BY display_name ASC;

-- name: ListEnabledOIDCProviders :many
SELECT * FROM oidc_providers
WHERE enabled
ORDER BY display_name ASC;

-- name: GetOIDCProvider :one
SELECT * FROM oidc_providers
WHERE id = $1;

-- name: GetOIDCProviderBySlug :one
SELECT * FROM oidc_providers
WHERE slug = $1;

-- name: CreateOIDCProvider :one
INSERT INTO oidc_providers (slug, display_name, issuer, client_id, client_secret, scopes, enabled, allow_signup)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateOIDCProvider :one
-- An empty client_secret means "leave the stored one alone": the value is never
-- read back to the admin screen, so an untouched field arrives empty and must
-- not be mistaken for a request to clear it. Clearing is done by switching the
-- provider to a public client, which sends the sentinel below.
UPDATE oidc_providers
SET display_name  = sqlc.arg(display_name),
    issuer        = sqlc.arg(issuer),
    client_id     = sqlc.arg(client_id),
    client_secret = CASE WHEN sqlc.arg(client_secret)::text = '' THEN client_secret ELSE sqlc.arg(client_secret)::text END,
    scopes        = sqlc.arg(scopes),
    enabled       = sqlc.arg(enabled),
    allow_signup  = sqlc.arg(allow_signup),
    updated_at    = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ClearOIDCProviderSecret :one
UPDATE oidc_providers
SET client_secret = '',
    updated_at    = now()
WHERE id = $1
RETURNING *;

-- name: DeleteOIDCProvider :exec
DELETE FROM oidc_providers
WHERE id = $1;

-- name: GetOIDCIdentity :one
SELECT * FROM oidc_identities
WHERE provider_id = $1 AND subject = $2;

-- name: ListOIDCIdentitiesForUser :many
SELECT i.*, p.slug, p.display_name
FROM oidc_identities i
JOIN oidc_providers p ON p.id = i.provider_id
WHERE i.user_id = $1
ORDER BY p.display_name ASC;

-- name: LinkOIDCIdentity :one
INSERT INTO oidc_identities (provider_id, subject, user_id, email)
VALUES ($1, $2, $3, $4)
ON CONFLICT (provider_id, subject) DO UPDATE
SET email      = EXCLUDED.email,
    last_login = now()
RETURNING *;

-- name: TouchOIDCIdentity :exec
UPDATE oidc_identities
SET last_login = now(),
    email      = $3
WHERE provider_id = $1 AND subject = $2;

-- name: GetUserByEmail :one
SELECT * FROM users
WHERE email = $1;

-- name: CreateFederatedUser :one
-- password_hash is left at its default (empty), which is not a valid bcrypt
-- hash, so this account can only ever be signed into through its provider.
INSERT INTO users (username, email, first_name, last_name, user_type)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;
