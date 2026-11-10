-- name: GetFSxImageByRootfs :one
SELECT * FROM fsx_images
WHERE rootfs = $1;

-- name: ClaimFSxImage :one
-- A build left by a task that died is taken over once it has gone quiet for
-- longer than any build could run.
INSERT INTO fsx_images (rootfs, osimage_id, size_bytes, claimed_by)
VALUES (sqlc.arg(rootfs), sqlc.arg(osimage_id), sqlc.arg(size_bytes), sqlc.arg(claimed_by))
ON CONFLICT (rootfs) DO UPDATE
SET claimed_by = EXCLUDED.claimed_by,
    updated_at = now()
WHERE fsx_images.status = 'building'
  AND (fsx_images.claimed_by IS NULL
       OR fsx_images.claimed_by = EXCLUDED.claimed_by
       OR fsx_images.updated_at < now() - interval '1 hour')
RETURNING *;

-- name: ReleaseFSxImageClaims :exec
UPDATE fsx_images
SET claimed_by = NULL, updated_at = now()
WHERE claimed_by = $1 AND status = 'building';

-- name: SetFSxImageVolume :exec
UPDATE fsx_images
SET volume_id = $2, volume_path = $3, updated_at = now()
WHERE id = $1;

-- name: SetFSxImageSnapshot :exec
UPDATE fsx_images
SET snapshot_id = $2, snapshot_arn = $3, updated_at = now()
WHERE id = $1;

-- name: MarkFSxImageReady :exec
UPDATE fsx_images
SET status = 'ready', claimed_by = NULL, updated_at = now()
WHERE id = $1;

-- name: ListUnusedFSxImages :many
-- Nothing is cloned from it and nothing new will be: its os image was
-- withdrawn or a newer build replaced it.
SELECT i.* FROM fsx_images i
LEFT JOIN osimages o ON o.id = i.osimage_id
WHERE NOT EXISTS (SELECT 1 FROM fsx_disks d WHERE d.image = i.id)
  AND (
    (i.status = 'building' AND i.updated_at < now() - interval '2 hours')
    OR (i.status = 'ready' AND (
        o.id IS NULL
        OR o.soft_deleted_at IS NOT NULL
        OR EXISTS (
            SELECT 1 FROM fsx_images n
            WHERE n.osimage_id = i.osimage_id
              AND n.status = 'ready'
              AND n.created_at > i.created_at
        )
    ))
  );

-- name: DeleteFSxImage :exec
DELETE FROM fsx_images
WHERE id = $1;

-- name: GetFSxDiskByVM :one
SELECT * FROM fsx_disks
WHERE vm = $1;

-- name: CreateFSxDisk :one
INSERT INTO fsx_disks (vm, image, volume_id, volume_path)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ReleaseFSxDiskForVM :exec
UPDATE fsx_disks
SET released = true
WHERE vm = $1;

-- name: ListReleasedFSxDisks :many
SELECT * FROM fsx_disks
WHERE vm IS NULL OR released;

-- name: DeleteFSxDisk :exec
DELETE FROM fsx_disks
WHERE id = $1;

-- name: ListFSxImagesAdmin :many
SELECT i.*,
       COALESCE(o.name, '')::text AS osimage_name,
       (SELECT count(*) FROM fsx_disks d WHERE d.image = i.id)::bigint AS disk_count
FROM fsx_images i
LEFT JOIN osimages o ON o.id = i.osimage_id
ORDER BY i.created_at DESC;

-- name: GetFSxImage :one
SELECT * FROM fsx_images
WHERE id = $1;

-- name: CountFSxDisksForImage :one
SELECT count(*) FROM fsx_disks
WHERE image = $1;

-- name: ListFSxDisksAdmin :many
SELECT d.*,
       COALESCE(v.name, '')::text AS vm_name,
       COALESCE(v.status, '')::text AS vm_status,
       COALESCE(i.rootfs, '')::text AS image_rootfs,
       (d.vm IS NOT NULL AND NOT d.released
        AND (v.disk_backend = 'fsx' OR v.disk_moving))::bool AS in_use
FROM fsx_disks d
LEFT JOIN fsx_images i ON i.id = d.image
LEFT JOIN vms v ON v.id = d.vm
ORDER BY d.created_at DESC;

-- name: GetFSxDisk :one
SELECT * FROM fsx_disks
WHERE id = $1;
