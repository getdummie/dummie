-- name: CreatePersonalAccessToken :one
INSERT INTO personal_access_tokens (user_id, token_hash, token_prefix, label, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: AuthenticatePersonalAccessToken :one
UPDATE personal_access_tokens
SET last_used_at = now()
WHERE token_hash = $1
  AND revoked = false
  AND (expires_at IS NULL OR expires_at > now())
RETURNING *;

-- name: ListPersonalAccessTokensByUser :many
SELECT * FROM personal_access_tokens
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: RevokePersonalAccessToken :execrows
UPDATE personal_access_tokens
SET revoked = true
WHERE id = $1 AND user_id = $2;

-- name: DeletePersonalAccessToken :execrows
DELETE FROM personal_access_tokens
WHERE id = $1 AND user_id = $2;
