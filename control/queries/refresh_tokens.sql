-- name: CreateRefreshToken :one
INSERT INTO refresh_tokens (user_id, token_hash, expires_at, user_agent, ip)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetRefreshTokenByHash :one
SELECT * FROM refresh_tokens
WHERE token_hash = $1
LIMIT 1;

-- name: RevokeRefreshTokenByHash :exec
UPDATE refresh_tokens
SET revoked = true
WHERE token_hash = $1;

-- name: RevokeAllForUser :exec
UPDATE refresh_tokens
SET revoked = true
WHERE user_id = $1 AND revoked = false;

-- name: CountRefreshTokens :one
SELECT count(*) FROM refresh_tokens;

-- name: ListRefreshTokens :many
SELECT
  rt.id,
  rt.user_id,
  u.username,
  u.email,
  rt.expires_at,
  rt.revoked,
  rt.user_agent,
  rt.ip,
  rt.created_at
FROM refresh_tokens rt
JOIN users u ON u.id = rt.user_id
ORDER BY rt.created_at DESC
LIMIT $1 OFFSET $2;

-- name: RevokeRefreshTokenByID :exec
UPDATE refresh_tokens
SET revoked = true
WHERE id = $1;

-- name: DeleteRefreshTokenByID :exec
DELETE FROM refresh_tokens
WHERE id = $1;

-- name: DeleteExpiredRefreshTokens :execrows
DELETE FROM refresh_tokens
WHERE expires_at < now();
