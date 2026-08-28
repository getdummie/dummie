
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

ALTER TABLE vms ADD CONSTRAINT vms_name_key UNIQUE (name);

ALTER TABLE vms ADD CONSTRAINT vms_name_shape CHECK (
    char_length(name) BETWEEN 3 AND 52
    AND name ~ '^[a-z0-9]+(-[a-z0-9]+)*$'
);

ALTER TABLE vms
    ADD COLUMN default_port INTEGER NOT NULL DEFAULT 8000
        CHECK (default_port BETWEEN 1 AND 65535);

ALTER TABLE vms
    ADD COLUMN public_ports INTEGER[] NOT NULL DEFAULT '{}';
