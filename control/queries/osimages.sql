-- name: ListOSImages :many
-- Withdrawn images are left out: the list is what an operator may still pick.
SELECT * FROM osimages
WHERE soft_deleted_at IS NULL
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountOSImages :one
SELECT count(*) FROM osimages
WHERE soft_deleted_at IS NULL;

-- name: GetOSImage :one
-- Withdrawn rows included: a link to one that was withdrawn should still open
-- its page and say so, rather than claim it never existed.
SELECT * FROM osimages
WHERE id = $1;

-- name: CreateOSImage :one
INSERT INTO osimages (name, description, object_key, file_name, size_bytes)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateOSImageDescription :one
-- The one editable field. Withdrawn rows are excluded, which returns no rows
-- and is what tells the caller it is closed rather than missing.
UPDATE osimages
SET description = $2
WHERE id = $1 AND soft_deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteOSImage :one
-- No-op on an already-withdrawn row, which returns no rows and lets the caller
-- tell "withdrawn now" apart from "was already gone".
UPDATE osimages
SET soft_deleted_at = now()
WHERE id = $1 AND soft_deleted_at IS NULL
RETURNING *;
