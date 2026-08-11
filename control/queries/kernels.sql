-- name: ListKernels :many
-- Withdrawn kernels are left out: the list is what an operator may still pick.
SELECT * FROM kernels
WHERE soft_deleted_at IS NULL
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountKernels :one
SELECT count(*) FROM kernels
WHERE soft_deleted_at IS NULL;

-- name: GetKernel :one
-- Withdrawn rows included: a link to one that was withdrawn should still open
-- its page and say so, rather than claim it never existed.
SELECT * FROM kernels
WHERE id = $1;

-- name: CreateKernel :one
INSERT INTO kernels (name, description, object_key, file_name, size_bytes)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateKernelDescription :one
-- The one editable field. Withdrawn rows are excluded, which returns no rows
-- and is what tells the caller it is closed rather than missing.
UPDATE kernels
SET description = $2
WHERE id = $1 AND soft_deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteKernel :one
-- No-op on an already-withdrawn row, which returns no rows and lets the caller
-- tell "withdrawn now" apart from "was already gone".
UPDATE kernels
SET soft_deleted_at = now()
WHERE id = $1 AND soft_deleted_at IS NULL
RETURNING *;
