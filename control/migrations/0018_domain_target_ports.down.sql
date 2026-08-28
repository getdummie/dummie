DELETE FROM vm_network_targets WHERE kind = 'domain' AND ports = 'none';
UPDATE vm_network_targets SET ports = '' WHERE kind = 'domain' AND ports <> '';

ALTER TABLE vm_network_targets DROP CONSTRAINT vm_network_targets_shape;

ALTER TABLE vm_network_targets ADD CONSTRAINT vm_network_targets_shape CHECK (
    (kind = 'domain' AND transport = '' AND ports = '')
    OR
    (kind = 'ip' AND transport <> '')
);
