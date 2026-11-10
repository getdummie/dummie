ALTER TABLE vms
    DROP COLUMN local_disk_kept,
    DROP COLUMN disk_moving;

DELETE FROM fsx_disks WHERE image IS NULL;
ALTER TABLE fsx_disks ALTER COLUMN image SET NOT NULL;
