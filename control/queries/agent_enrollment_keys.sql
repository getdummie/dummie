-- name: CreateEnrollmentKey :one
INSERT INTO agent_enrollment_keys (key_hash, key_prefix, label, max_uses, expires_at, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ConsumeEnrollmentKey :one
-- ConsumeEnrollmentKey claims one use of a key. Validity (not revoked, not
-- expired, uses remaining) is checked in the same statement that increments the
-- counter, so two agents racing on a single-use key cannot both win. Zero rows
-- means unknown/revoked/expired/exhausted -- the caller must not distinguish.
UPDATE agent_enrollment_keys
SET uses = uses + 1
WHERE key_hash = $1
  AND revoked = false
  AND (expires_at IS NULL OR expires_at > now())
  AND (max_uses IS NULL OR uses < max_uses)
RETURNING *;

-- name: CountEnrollmentKeys :one
SELECT count(*) FROM agent_enrollment_keys;

-- name: ListEnrollmentKeys :many
SELECT * FROM agent_enrollment_keys
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: RevokeEnrollmentKey :exec
UPDATE agent_enrollment_keys
SET revoked = true
WHERE id = $1;

-- name: DeleteEnrollmentKey :exec
DELETE FROM agent_enrollment_keys
WHERE id = $1;
