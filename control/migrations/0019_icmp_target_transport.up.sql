-- 0019_icmp_target_transport: 'icmp' becomes a transport an allowance can name.
--
-- Ping is the first thing anyone reaches for to find out whether a guest has a
-- network, and until now the only way to allow it was transport 'any', which
-- compiles to `pass ip` -- every tcp port and every udp port to that address as
-- well. So the honest options were "no ping" or "everything", and the answer to
-- "can this VM ping 8.8.8.8" was decided by which of those a user picked.
--
-- Suricata takes icmp in a rule header, so this compiles to exactly what it says:
--
--     pass icmp 10.64.0.5 any -> 8.8.8.8 any (...)
--
-- Ports stay empty. icmp has none -- the type and code sit where a port would be,
-- and a rule header cannot address them -- so a value there would be a promise the
-- generator cannot keep, which is the same reason 0010 forbade ports on a domain.

-- The transport check in 0010 was written inline on the column, so its name was
-- generated rather than chosen. Found rather than guessed: a DROP ... IF EXISTS on
-- the wrong name would do nothing at all and leave the old constraint enforcing the
-- narrower list, which is a migration that reports success and changes nothing.
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

-- Named this time, so 0020 does not have to do the above again.
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
