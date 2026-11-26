CREATE TABLE destination_groups (
    id          UUID NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE destination_group_entries (
    id       UUID NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL REFERENCES destination_groups(id) ON DELETE CASCADE,

    kind        TEXT NOT NULL CHECK (kind IN ('domain', 'ip')),
    destination TEXT NOT NULL,
    transport   TEXT NOT NULL DEFAULT '' CHECK (transport IN ('', 'tcp', 'udp', 'any', 'icmp')),
    ports       TEXT NOT NULL DEFAULT '',
    note        TEXT NOT NULL DEFAULT '',

    CONSTRAINT destination_group_entries_shape CHECK (
        (kind = 'domain' AND transport = '' AND ports IN ('', '80', '443', '80,443', 'none'))
        OR
        (kind = 'ip' AND transport IN ('tcp', 'udp', 'any'))
        OR
        (kind = 'ip' AND transport = 'icmp' AND ports = '')
    )
);

CREATE UNIQUE INDEX idx_destination_group_entries_unique
    ON destination_group_entries (group_id, destination, transport, ports);

CREATE TABLE vm_destination_groups (
    id         UUID NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
    vm_id      UUID NOT NULL REFERENCES vms(id) ON DELETE CASCADE,
    group_id   UUID NOT NULL REFERENCES destination_groups(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (vm_id, group_id)
);

CREATE INDEX idx_vm_destination_groups_group_id ON vm_destination_groups (group_id);
