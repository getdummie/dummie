-- name: ListOSImages :many
SELECT * FROM osimages
WHERE soft_deleted_at IS NULL
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListReadyOSImages :many
SELECT * FROM osimages
WHERE soft_deleted_at IS NULL AND status = 'ready'
ORDER BY created_at DESC
LIMIT $1;

-- name: CountOSImages :one
SELECT count(*) FROM osimages
WHERE soft_deleted_at IS NULL;

-- name: GetOSImage :one
SELECT * FROM osimages
WHERE id = $1;

-- name: CreateOSImage :one
INSERT INTO osimages (name, description, object_key, file_name, size_bytes, source, status)
VALUES ($1, $2, $3, $4, $5, 'upload', 'ready')
RETURNING *;

-- name: CreateOCIOSImage :one
INSERT INTO osimages (name, description, oci_ref, created_by, source, status)
VALUES ($1, $2, $3, sqlc.narg(created_by), 'oci', 'pending')
RETURNING *;

-- name: ListOSImagesByOwner :many
SELECT * FROM osimages
WHERE soft_deleted_at IS NULL AND created_by = $1
ORDER BY created_at DESC
LIMIT $2;

-- name: CountLiveOSImagesByOwner :one
SELECT count(*) FROM osimages
WHERE soft_deleted_at IS NULL AND created_by = $1;

-- name: CountBuildingOSImagesByOwner :one
SELECT count(*) FROM osimages
WHERE soft_deleted_at IS NULL AND created_by = $1
  AND status IN ('pending', 'building');

-- name: SoftDeleteOSImageForOwner :one
UPDATE osimages
SET soft_deleted_at = now()
WHERE id = $1 AND created_by = $2 AND soft_deleted_at IS NULL
RETURNING *;

-- name: MarkOSImageBuilding :one
UPDATE osimages
SET status = 'building', status_detail = ''
WHERE id = $1 AND soft_deleted_at IS NULL AND status IN ('pending', 'building', 'failed')
RETURNING *;

-- name: FinishOSImageBuild :one
UPDATE osimages
SET status = 'ready',
    status_detail = '',
    object_key = sqlc.arg(object_key),
    file_name = sqlc.arg(file_name),
    size_bytes = sqlc.arg(size_bytes),
    oci_digest = sqlc.arg(oci_digest),
    config_user = sqlc.arg(config_user),
    config_entrypoint = sqlc.arg(config_entrypoint)::text[],
    config_cmd = sqlc.arg(config_cmd)::text[],
    config_env = sqlc.arg(config_env)::text[],
    config_exposed_ports = sqlc.arg(config_exposed_ports)::integer[],
    default_port = COALESCE(default_port, sqlc.narg(default_port)::integer)
WHERE id = sqlc.arg(id) AND soft_deleted_at IS NULL AND status <> 'ready'
RETURNING *;

-- name: FailOSImageBuild :exec
UPDATE osimages
SET status = 'failed', status_detail = sqlc.arg(status_detail)
WHERE id = sqlc.arg(id) AND soft_deleted_at IS NULL AND status <> 'ready';

-- name: UpdateOSImage :one
UPDATE osimages
SET description = sqlc.arg(description),
    default_port = COALESCE(sqlc.narg(default_port)::integer, default_port)
WHERE id = sqlc.arg(id) AND soft_deleted_at IS NULL
RETURNING *;

-- name: UpdateOSImageConfig :one
UPDATE osimages
SET config_user = sqlc.arg(config_user),
    config_entrypoint = sqlc.arg(config_entrypoint)::text[],
    config_cmd = sqlc.arg(config_cmd)::text[],
    config_env = sqlc.arg(config_env)::text[],
    config_exposed_ports = sqlc.arg(config_exposed_ports)::integer[]
WHERE id = sqlc.arg(id) AND soft_deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteOSImage :one
UPDATE osimages
SET soft_deleted_at = now()
WHERE id = $1 AND soft_deleted_at IS NULL
RETURNING *;
