-- name: GetCustomDomainByVM :one
SELECT * FROM vm_custom_domains
WHERE vm_id = $1;

-- name: GetCustomDomain :one
SELECT * FROM vm_custom_domains
WHERE id = $1;

-- name: GetCustomDomainByDomain :one
SELECT * FROM vm_custom_domains
WHERE domain = $1;

-- name: CreateCustomDomain :one
INSERT INTO vm_custom_domains (vm_id, domain)
VALUES ($1, $2)
RETURNING *;

-- name: DeleteCustomDomain :exec
DELETE FROM vm_custom_domains
WHERE id = $1;

-- name: SetCustomDomainStatus :one
UPDATE vm_custom_domains
SET status     = $2,
    last_error = $3,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkCustomDomainOrdered :exec
UPDATE vm_custom_domains
SET ordered_at = now()
WHERE id = $1;

-- name: UpdateCustomDomainCert :one
UPDATE vm_custom_domains
SET status           = 'active',
    last_error       = '',
    cert_object_key  = $2,
    key_object_key   = $3,
    cert_fingerprint = $4,
    cert_not_after   = $5,
    cert_issued_at   = now(),
    ordered_at       = NULL,
    updated_at       = now()
WHERE id = $1
RETURNING *;

-- name: ReuseCustomDomainCert :one
UPDATE vm_custom_domains
SET status           = 'active',
    last_error       = '',
    cert_object_key  = $2,
    key_object_key   = $3,
    cert_fingerprint = $4,
    cert_not_after   = $5,
    cert_issued_at   = $6,
    ordered_at       = NULL,
    updated_at       = now()
WHERE id = $1
RETURNING *;

-- name: GetCustomDomainOwner :one
SELECT v.created_by
FROM vm_custom_domains cd
JOIN vms v ON v.id = cd.vm_id
WHERE cd.id = $1;

-- name: GetCustomDomainClient :one
SELECT v.client_id
FROM vm_custom_domains cd
JOIN vms v ON v.id = cd.vm_id
WHERE cd.id = $1;

-- name: ListCustomDomains :many
SELECT cd.*,
       v.name                          AS vm_name,
       v.status                        AS vm_status,
       COALESCE(u.username, '')::text  AS owner_username,
       COALESCE(u.email, '')::text     AS owner_email,
       COALESCE(cl.hostname, '')::text AS client_hostname
FROM vm_custom_domains cd
JOIN vms v ON v.id = cd.vm_id
LEFT JOIN users u ON u.id = v.created_by
LEFT JOIN clients cl ON cl.id = v.client_id
ORDER BY cd.domain;

-- name: GetCustomDomainForIssue :one
SELECT cd.*,
       v.name       AS vm_name,
       v.client_id  AS client_id,
       v.status     AS vm_status,
       COALESCE(d.tld, '')::text            AS domain_tld,
       COALESCE(d.acme_email, '')::text     AS acme_email,
       COALESCE(d.acme_directory, '')::text AS acme_directory
FROM vm_custom_domains cd
JOIN vms v ON v.id = cd.vm_id
JOIN clients c ON c.id = v.client_id
LEFT JOIN domains d ON d.id = c.domain_id
WHERE cd.id = $1;

-- name: ListCustomDomainRoutesByClient :many
SELECT cd.domain, v.name AS vm_name, v.ip AS vm_ip,
       v.default_port, v.public_ports,
       COALESCE(NULLIF(v.default_user, ''), v.spec->'image'->>'user', '')::text AS session_user
FROM vm_custom_domains cd
JOIN vms v ON v.id = cd.vm_id
WHERE v.client_id = $1
  AND cd.status = 'active'
  AND v.ip <> ''
  AND v.status <> 'gone'
ORDER BY cd.domain;

-- name: ListCustomDomainCertsByClient :many
SELECT cd.domain, cd.cert_object_key, cd.key_object_key
FROM vm_custom_domains cd
JOIN vms v ON v.id = cd.vm_id
WHERE v.client_id = $1
  AND cd.status = 'active'
  AND cd.cert_object_key <> ''
  AND cd.key_object_key <> ''
ORDER BY cd.domain;

-- name: CustomDomainIsServed :one
SELECT EXISTS (
    SELECT 1 FROM vm_custom_domains
    WHERE domain = $1 AND status = 'active'
)::boolean;

-- name: GetVMForOwnerByCustomDomain :one
SELECT v.id
FROM vms v
JOIN vm_custom_domains cd ON cd.vm_id = v.id
WHERE cd.domain = sqlc.arg(domain)
  AND cd.status = 'active'
  AND v.status <> 'gone'
  AND (sqlc.arg(is_admin)::boolean OR v.created_by = sqlc.arg(owner_id));

-- name: ListCustomDomainsDueForRenewal :many
SELECT * FROM vm_custom_domains
WHERE status = 'active'
  AND (cert_not_after IS NULL OR cert_not_after < now() + make_interval(secs => sqlc.arg(within_seconds)::float))
ORDER BY cert_not_after NULLS FIRST;
