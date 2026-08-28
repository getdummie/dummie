-- name: ListOSImages :many
SELECT * FROM osimages
WHERE soft_deleted_at IS NULL
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountOSImages :one
SELECT count(*) FROM osimages
WHERE soft_deleted_at IS NULL;

-- name: GetOSImage :one
SELECT * FROM osimages
WHERE id = $1;

-- name: CreateOSImage :one
INSERT INTO osimages (name, description, object_key, file_name, size_bytes)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateOSImageDescription :one
UPDATE osimages
SET description = $2
WHERE id = $1 AND soft_deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteOSImage :one
UPDATE osimages
SET soft_deleted_at = now()
WHERE id = $1 AND soft_deleted_at IS NULL
RETURNING *;
