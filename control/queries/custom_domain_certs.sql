-- name: GetCustomDomainCert :one
SELECT * FROM custom_domain_certs
WHERE domain = $1;

-- name: UpsertCustomDomainCert :one
INSERT INTO custom_domain_certs
    (domain, owner_id, cert_object_key, key_object_key, cert_fingerprint, cert_not_after)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (domain) DO UPDATE
SET owner_id         = EXCLUDED.owner_id,
    cert_object_key  = EXCLUDED.cert_object_key,
    key_object_key   = EXCLUDED.key_object_key,
    cert_fingerprint = EXCLUDED.cert_fingerprint,
    cert_not_after   = EXCLUDED.cert_not_after,
    cert_issued_at   = now(),
    updated_at       = now()
RETURNING *;

-- name: DeleteCustomDomainCert :exec
DELETE FROM custom_domain_certs
WHERE id = $1;

-- name: GetCustomDomainCertByID :one
SELECT * FROM custom_domain_certs
WHERE id = $1;

-- name: ListCustomDomainCerts :many
SELECT c.*,
       COALESCE(u.username, '')::text      AS owner_username,
       COALESCE(u.email, '')::text         AS owner_email,
       COALESCE(v.name, '')::text          AS vm_name,
       COALESCE(cd.status, '')::text       AS domain_status
FROM custom_domain_certs c
LEFT JOIN users u ON u.id = c.owner_id
LEFT JOIN vm_custom_domains cd ON cd.domain = c.domain
LEFT JOIN vms v ON v.id = cd.vm_id
ORDER BY c.domain;

-- Reclaimable means expired with no vm bound to the domain any longer. One that
-- is still bound is left alone: the renewal sweep owns it.
-- name: ListReclaimableCustomDomainCerts :many
SELECT * FROM custom_domain_certs
WHERE cert_not_after < now()
  AND NOT EXISTS (
      SELECT 1 FROM vm_custom_domains cd WHERE cd.domain = custom_domain_certs.domain
  );
