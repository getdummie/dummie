-- A disk moved off a host has no golden image to have been cloned from.
ALTER TABLE fsx_disks ALTER COLUMN image DROP NOT NULL;

ALTER TABLE vms
    ADD COLUMN disk_moving BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN local_disk_kept BOOLEAN NOT NULL DEFAULT false;
