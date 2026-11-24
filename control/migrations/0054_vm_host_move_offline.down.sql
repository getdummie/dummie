DROP TABLE vm_host_evictions;

ALTER TABLE fsx_disks DROP COLUMN nfs_client;

ALTER TABLE vms DROP COLUMN boot_record;
