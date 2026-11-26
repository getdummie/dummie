-- A deleting host is revoked and kept only until its fsx vms have moved off.
ALTER TABLE clients ADD COLUMN deleting BOOLEAN NOT NULL DEFAULT false;

UPDATE fsx_disks SET vm = NULL WHERE released;

ALTER TABLE fsx_disks
    DROP CONSTRAINT fsx_disks_vm_fkey,
    ADD CONSTRAINT fsx_disks_vm_fkey FOREIGN KEY (vm) REFERENCES vms(id) ON DELETE RESTRICT;
