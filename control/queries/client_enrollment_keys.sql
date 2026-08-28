-- name: CreateEnrollmentKey :one
INSERT INTO client_enrollment_keys (key_hash, key_prefix, label, max_uses, expires_at, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ConsumeEnrollmentKey :one
UPDATE client_enrollment_keys
SET uses = uses + 1
WHERE key_hash = $1
  AND revoked = false
  AND (expires_at IS NULL OR expires_at > now())
  AND (max_uses IS NULL OR uses < max_uses)
RETURNING *;

-- name: CountEnrollmentKeys :one
SELECT count(*) FROM client_enrollment_keys;

-- name: ListEnrollmentKeys :many
SELECT * FROM client_enrollment_keys
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: RevokeEnrollmentKey :exec
UPDATE client_enrollment_keys
SET revoked = true
WHERE id = $1;

-- name: DeleteEnrollmentKey :exec
DELETE FROM client_enrollment_keys
WHERE id = $1;
