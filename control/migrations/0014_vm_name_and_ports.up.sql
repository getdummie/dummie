-- 0014_vm_name_and_ports: the name becomes the VM's routed identity, and the
-- ports it publishes become part of the row.
--
-- name was free text and could repeat. It is now the key proxy routes http by,
-- which makes it a fleet-wide identifier: two VMs sharing a name would be two
-- guests answering to one route. Hence unique, bounded in length, and restricted
-- to the shape of a hostname label.

-- Existing rows first: the constraints below cannot be added over data that
-- violates them. A name that is already usable and already unique is kept --
-- renaming a VM someone is using would be a worse outcome than an ugly name on
-- the rows that have to be rewritten. Everything else becomes 'vm-<id>', which
-- is unique by construction and 39 characters long.
UPDATE vms SET name = 'vm-' || id::text
WHERE name !~ '^[a-z0-9]+(-[a-z0-9]+)*$'
   OR char_length(name) < 3
   OR char_length(name) > 52
   OR id IN (
        SELECT id FROM (
            SELECT id, row_number() OVER (PARTITION BY name ORDER BY created_at, id) AS rn
            FROM vms
        ) dupes WHERE rn > 1
      );

-- Named, because the create path retries on exactly this constraint and matches
-- it by name to tell a taken name apart from any other unique violation.
ALTER TABLE vms ADD CONSTRAINT vms_name_key UNIQUE (name);

-- Lowercase alphanumeric words joined by single hyphens: no leading, trailing or
-- doubled hyphen. That is a DNS label, which is what the name has to be able to
-- become. The bounds are the operator's: long enough to be meaningful, short
-- enough to stay inside a 63-character label once anything is appended to it.
ALTER TABLE vms ADD CONSTRAINT vms_name_shape CHECK (
    char_length(name) BETWEEN 3 AND 52
    AND name ~ '^[a-z0-9]+(-[a-z0-9]+)*$'
);

-- The port inside the guest a request goes to when nothing says otherwise. 8000
-- rather than 80: these are application servers, and a guest listening on a
-- privileged port would have to be running something as root to do it.
ALTER TABLE vms
    ADD COLUMN default_port INTEGER NOT NULL DEFAULT 8000
        CHECK (default_port BETWEEN 1 AND 65535);

-- Every port the VM publishes. An array rather than a side table: it is a short
-- list that is only ever read and written whole, as part of the row, and a table
-- would add a join to the one query that has to stay cheap -- the proxy config
-- is regenerated on every inventory tick from every client.
--
-- Empty by default: a VM publishes nothing until someone says which port.
ALTER TABLE vms
    ADD COLUMN public_ports INTEGER[] NOT NULL DEFAULT '{}';
