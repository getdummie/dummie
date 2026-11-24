ALTER TABLE vms ADD COLUMN boot_record JSONB;

ALTER TABLE fsx_disks ADD COLUMN nfs_client TEXT NOT NULL DEFAULT '';

-- A host a vm was moved off while it was offline. It still holds a stale copy
-- under vm_id, which it is told to delete when it reconnects.
CREATE TABLE vm_host_evictions (
    client_id  UUID NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    vm_id      TEXT NOT NULL,
    vm         UUID REFERENCES vms(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (client_id, vm_id)
);
