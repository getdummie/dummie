-- name: GetSetting :one
SELECT * FROM settings
WHERE key = $1;

-- name: ListSettings :many
SELECT * FROM settings
ORDER BY key;

-- name: UpsertSetting :one
-- The row is seeded by the migration, but an upsert keeps a setting writable on
-- a database where the seed was removed or the key was added after the fact.
INSERT INTO settings (key, value, updated_by)
VALUES ($1, $2, $3)
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value,
    updated_at = now(),
    updated_by = EXCLUDED.updated_by
RETURNING *;
