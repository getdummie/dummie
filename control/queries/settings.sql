-- name: GetSetting :one
SELECT * FROM settings
WHERE key = $1;

-- name: ListSettings :many
SELECT * FROM settings
ORDER BY key;

-- name: UpsertSetting :one
INSERT INTO settings (key, value, updated_by)
VALUES ($1, $2, $3)
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value,
    updated_at = now(),
    updated_by = EXCLUDED.updated_by
RETURNING *;

-- name: InsertSettingIfAbsent :one
INSERT INTO settings (key, value)
VALUES ($1, $2)
ON CONFLICT (key) DO UPDATE
SET value = settings.value
RETURNING *;
