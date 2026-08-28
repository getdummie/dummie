CREATE TABLE vm_network_targets (
    id    UUID NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
    vm_id UUID NOT NULL REFERENCES vms(id) ON DELETE CASCADE,

    kind TEXT NOT NULL CHECK (kind IN ('domain', 'ip')),

    destination TEXT NOT NULL,

    transport TEXT NOT NULL DEFAULT ''
              CHECK (transport IN ('', 'tcp', 'udp', 'any')),

    ports TEXT NOT NULL DEFAULT '',

    note TEXT NOT NULL DEFAULT '',

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT vm_network_targets_shape CHECK (
        (kind = 'domain' AND transport = '' AND ports = '')
        OR
        (kind = 'ip' AND transport <> '')
    )
);

CREATE INDEX idx_vm_network_targets_vm_id ON vm_network_targets (vm_id);

CREATE UNIQUE INDEX idx_vm_network_targets_unique
    ON vm_network_targets (vm_id, destination, transport, ports);
