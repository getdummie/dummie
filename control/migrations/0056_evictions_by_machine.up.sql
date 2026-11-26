-- Keyed on the machine rather than the client row, so a host that is deleted
-- and enrolls again is still told to drop its stale copies.
ALTER TABLE vm_host_evictions ADD COLUMN machine_id TEXT;

UPDATE vm_host_evictions e
SET machine_id = c.machine_id
FROM clients c
WHERE c.id = e.client_id;

DELETE FROM vm_host_evictions WHERE machine_id IS NULL;

ALTER TABLE vm_host_evictions
    DROP CONSTRAINT vm_host_evictions_pkey,
    DROP COLUMN client_id,
    ALTER COLUMN machine_id SET NOT NULL,
    ADD PRIMARY KEY (machine_id, vm_id);
