-- 0010_vm_network_targets: the destinations a VM is expected to talk to.
--
-- Recorded only. Nothing reads these yet; they are the input a later pass will
-- compile into Suricata rules scoped to one VM.
--
-- The two kinds take DISJOINT fields, because they compile to different rules:
--
--   domain -> app-layer inspection, address and port unknowable at rule time:
--       pass dns  $HOME_NET any -> any any (dns.query;  content:"x"; endswith;)
--       pass tls  $HOME_NET any -> any any (tls.sni;    content:"x"; endswith;)
--       pass http $HOME_NET any -> any any (http.host;  content:"x"; endswith;)
--     There is nowhere in those rules to put a transport or a port. A domain
--     resolves to addresses the rule engine never sees, so scoping by either
--     would be recording a constraint that can never be applied.
--
--   ip -> a rule header, where transport and port are exactly what goes in:
--       pass tcp  $HOME_NET any -> 1.2.3.4 443 (...)
--
-- Hence the shape constraint at the bottom: a domain row carrying a port is not
-- a harmless extra, it is a promise the generator cannot keep.
CREATE TABLE vm_network_targets (
    id    UUID NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
    vm_id UUID NOT NULL REFERENCES vms(id) ON DELETE CASCADE,

    -- Which of the two shapes this row is. Stored rather than re-derived, so
    -- the generator cannot reach a different answer than the API did.
    kind TEXT NOT NULL CHECK (kind IN ('domain', 'ip')),

    -- A hostname for 'domain'; an IP or CIDR for 'ip'. Kept as written: the
    -- generator has to branch on kind anyway, and one column keeps what the
    -- user actually asked for recoverable.
    destination TEXT NOT NULL,

    -- 'ip' only. 'any' becomes Suricata's `ip` protocol, which covers both,
    -- rather than two near-identical rules. '' for a domain row.
    transport TEXT NOT NULL DEFAULT ''
              CHECK (transport IN ('', 'tcp', 'udp', 'any')),

    -- 'ip' only, in Suricata's port syntax: '443', '80,443', '1000:2000'.
    -- Kept as text because parsing to integers throws away lists and ranges the
    -- rule language accepts natively. '' means any port. '' for a domain row.
    ports TEXT NOT NULL DEFAULT '',

    -- Why this destination is allowed. Not decoration: an allowlist nobody can
    -- explain is one nobody can safely prune later. It is also the natural
    -- source for the rule's msg: field.
    note TEXT NOT NULL DEFAULT '',

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- '' rather than NULL for the inapplicable columns, matching how the rest of
    -- the schema handles "no value": every consumer reads a string without
    -- checking a flag, and a unique index over them behaves (NULLs would each
    -- compare distinct and let duplicates through).
    CONSTRAINT vm_network_targets_shape CHECK (
        (kind = 'domain' AND transport = '' AND ports = '')
        OR
        (kind = 'ip' AND transport <> '')
    )
);

CREATE INDEX idx_vm_network_targets_vm_id ON vm_network_targets (vm_id);

-- The same destination on the same transport and ports twice is a duplicate
-- rule, not a second intention.
CREATE UNIQUE INDEX idx_vm_network_targets_unique
    ON vm_network_targets (vm_id, destination, transport, ports);
