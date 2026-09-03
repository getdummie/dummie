-- A certificate outlives the vm it was obtained for. vm_custom_domains is
-- cascade-deleted with its vm, so the keys it held would be orphaned in object
-- storage; this table owns the blobs instead and the binding merely points at
-- them. A user who rebuilds a vm under the same name gets the same certificate
-- back and only has to move the CNAME.
CREATE TABLE custom_domain_certs (
    id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain   TEXT NOT NULL UNIQUE,
    owner_id UUID REFERENCES users(id) ON DELETE SET NULL,

    cert_object_key  TEXT NOT NULL,
    key_object_key   TEXT NOT NULL,
    cert_fingerprint TEXT NOT NULL DEFAULT '',
    cert_not_after   TIMESTAMPTZ NOT NULL,
    cert_issued_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_custom_domain_certs_expiry ON custom_domain_certs (cert_not_after);

INSERT INTO custom_domain_certs
    (domain, owner_id, cert_object_key, key_object_key, cert_fingerprint, cert_not_after, cert_issued_at)
SELECT cd.domain, v.created_by, cd.cert_object_key, cd.key_object_key,
       cd.cert_fingerprint, cd.cert_not_after, COALESCE(cd.cert_issued_at, now())
FROM vm_custom_domains cd
JOIN vms v ON v.id = cd.vm_id
WHERE cd.cert_object_key <> ''
  AND cd.key_object_key <> ''
  AND cd.cert_not_after IS NOT NULL
ON CONFLICT (domain) DO NOTHING;
