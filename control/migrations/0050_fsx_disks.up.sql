ALTER TABLE vms
    ADD COLUMN disk_backend TEXT NOT NULL DEFAULT 'local'
        CHECK (disk_backend IN ('local', 'fsx'));

-- rootfs is the host's cache name for the ext4, which already keys the tar and
-- everything baked into it, so vms share an image exactly when a host would.
CREATE TABLE fsx_images (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rootfs       TEXT NOT NULL UNIQUE,
    osimage_id   UUID REFERENCES osimages(id) ON DELETE SET NULL,
    size_bytes   BIGINT NOT NULL DEFAULT 0,
    volume_id    TEXT NOT NULL DEFAULT '',
    volume_path  TEXT NOT NULL DEFAULT '',
    snapshot_id  TEXT NOT NULL DEFAULT '',
    snapshot_arn TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'building'
                 CHECK (status IN ('building', 'ready')),
    claimed_by   UUID,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX fsx_images_osimage_idx ON fsx_images (osimage_id);

-- vm goes null on delete so the volume outlives the row until it is cleaned up;
-- image has no cascade so an image cannot go while a clone of it exists.
CREATE TABLE fsx_disks (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vm          UUID UNIQUE REFERENCES vms(id) ON DELETE SET NULL,
    image       UUID NOT NULL REFERENCES fsx_images(id),
    volume_id   TEXT NOT NULL,
    volume_path TEXT NOT NULL,
    released    BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX fsx_disks_image_idx ON fsx_disks (image);
