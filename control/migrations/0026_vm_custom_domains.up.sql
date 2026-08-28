CREATE TABLE vm_custom_domains (
    id     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vm_id  UUID NOT NULL UNIQUE REFERENCES vms(id) ON DELETE CASCADE,
    domain TEXT NOT NULL UNIQUE,

    status TEXT NOT NULL DEFAULT 'pending_dns'
           CHECK (status IN ('pending_dns', 'verifying', 'issuing', 'active', 'failed')),

    last_error TEXT NOT NULL DEFAULT '',

    cert_object_key  TEXT NOT NULL DEFAULT '',
    key_object_key   TEXT NOT NULL DEFAULT '',
    cert_fingerprint TEXT NOT NULL DEFAULT '',
    cert_not_after   TIMESTAMPTZ,
    cert_issued_at   TIMESTAMPTZ,

    -- When a host was last handed an order for this name. Tracked apart from
    -- status because a renewal runs while the domain is still being served,
    -- and status is what decides whether it is served.
    ordered_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE vm_custom_domains ADD CONSTRAINT vm_custom_domains_shape CHECK (
    char_length(domain) BETWEEN 4 AND 253
    AND domain ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$'
);

CREATE INDEX idx_vm_custom_domains_renewal
    ON vm_custom_domains (cert_not_after)
    WHERE status = 'active';
