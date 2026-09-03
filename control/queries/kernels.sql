-- name: ListKernels :many
SELECT * FROM kernels
WHERE soft_deleted_at IS NULL
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountKernels :one
SELECT count(*) FROM kernels
WHERE soft_deleted_at IS NULL;

-- name: ListWithdrawnKernels :many
SELECT * FROM kernels
WHERE soft_deleted_at IS NOT NULL
ORDER BY soft_deleted_at DESC
LIMIT $1 OFFSET $2;

-- name: CountWithdrawnKernels :one
SELECT count(*) FROM kernels
WHERE soft_deleted_at IS NOT NULL;

-- name: GetKernel :one
SELECT * FROM kernels
WHERE id = $1;

-- name: CreateKernel :one
INSERT INTO kernels (name, description, object_key, file_name, size_bytes)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateKernelDescription :one
UPDATE kernels
SET description = $2
WHERE id = $1 AND soft_deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteKernel :one
UPDATE kernels
SET soft_deleted_at = now()
WHERE id = $1 AND soft_deleted_at IS NULL
RETURNING *;

-- name: PurgeKernel :one
-- Only a withdrawn kernel can go: the row is dropped for good so its name frees
-- up, and the caller deletes the object it returns from the bucket.
DELETE FROM kernels
WHERE id = $1 AND soft_deleted_at IS NOT NULL
RETURNING *;
