ALTER TABLE vm_host_evictions ADD COLUMN client_id UUID REFERENCES clients(id) ON DELETE CASCADE;

UPDATE vm_host_evictions e
SET client_id = c.id
FROM clients c
WHERE c.machine_id = e.machine_id;

DELETE FROM vm_host_evictions WHERE client_id IS NULL;

ALTER TABLE vm_host_evictions
    DROP CONSTRAINT vm_host_evictions_pkey,
    DROP COLUMN machine_id,
    ALTER COLUMN client_id SET NOT NULL,
    ADD PRIMARY KEY (client_id, vm_id);
