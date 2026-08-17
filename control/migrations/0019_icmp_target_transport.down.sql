-- icmp rows cannot survive the narrower constraint, and widening them to 'any'
-- would grant every tcp and udp port to that address -- more than the row ever
-- said. Removed instead: on the way down, an allowance that cannot be expressed
-- should stop existing rather than quietly become a bigger one.
DELETE FROM vm_network_targets WHERE transport = 'icmp';

ALTER TABLE vm_network_targets DROP CONSTRAINT vm_network_targets_shape;
ALTER TABLE vm_network_targets DROP CONSTRAINT vm_network_targets_transport_check;

ALTER TABLE vm_network_targets
    ADD CONSTRAINT vm_network_targets_transport_check
    CHECK (transport IN ('', 'tcp', 'udp', 'any'));

ALTER TABLE vm_network_targets ADD CONSTRAINT vm_network_targets_shape CHECK (
    (kind = 'domain' AND transport = '' AND ports IN ('', '80', '443', '80,443', 'none'))
    OR
    (kind = 'ip' AND transport <> '')
);
