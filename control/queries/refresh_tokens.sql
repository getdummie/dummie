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
