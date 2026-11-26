ALTER TABLE fsx_disks
    DROP CONSTRAINT fsx_disks_vm_fkey,
    ADD CONSTRAINT fsx_disks_vm_fkey FOREIGN KEY (vm) REFERENCES vms(id) ON DELETE SET NULL;

ALTER TABLE clients DROP COLUMN deleting;
