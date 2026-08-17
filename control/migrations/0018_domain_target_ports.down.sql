-- The old constraint cannot be added over rows that carry ports on a domain, so
-- they are cleared first. A '443' or '80' row widens back to both web ports,
-- which is the closest the old shape can express. A 'none' row is deleted rather
-- than widened: it exists to allow a lookup and nothing else, and turning it into
-- full web access would grant something nobody asked for on the way down.
DELETE FROM vm_network_targets WHERE kind = 'domain' AND ports = 'none';
UPDATE vm_network_targets SET ports = '' WHERE kind = 'domain' AND ports <> '';

ALTER TABLE vm_network_targets DROP CONSTRAINT vm_network_targets_shape;

ALTER TABLE vm_network_targets ADD CONSTRAINT vm_network_targets_shape CHECK (
    (kind = 'domain' AND transport = '' AND ports = '')
    OR
    (kind = 'ip' AND transport <> '')
);
