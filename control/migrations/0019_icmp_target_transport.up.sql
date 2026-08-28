
DO $$
DECLARE
    name text;
BEGIN
    SELECT conname INTO name
    FROM pg_constraint
    WHERE conrelid = 'vm_network_targets'::regclass
      AND contype = 'c'
      AND conname <> 'vm_network_targets_shape'
      AND pg_get_constraintdef(oid) LIKE '%transport%';

    IF name IS NULL THEN
        RAISE EXCEPTION 'could not find the transport check constraint on vm_network_targets';
    END IF;
    EXECUTE format('ALTER TABLE vm_network_targets DROP CONSTRAINT %I', name);
END $$;

ALTER TABLE vm_network_targets
    ADD CONSTRAINT vm_network_targets_transport_check
    CHECK (transport IN ('', 'tcp', 'udp', 'any', 'icmp'));

ALTER TABLE vm_network_targets DROP CONSTRAINT vm_network_targets_shape;

ALTER TABLE vm_network_targets ADD CONSTRAINT vm_network_targets_shape CHECK (
    (kind = 'domain' AND transport = '' AND ports IN ('', '80', '443', '80,443', 'none'))
    OR
    (kind = 'ip' AND transport IN ('tcp', 'udp', 'any'))
    OR
    (kind = 'ip' AND transport = 'icmp' AND ports = '')
);
