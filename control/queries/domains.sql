-- name: ListDomains :many
SELECT * FROM domains
ORDER BY tld;

-- name: CountDomains :one
SELECT count(*) FROM domains;

-- name: CreateDomain :one
INSERT INTO domains (tld)
VALUES ($1)
RETURNING *;

-- name: DeleteDomain :exec
DELETE FROM domains
WHERE id = $1;

-- name: GetDomain :one
SELECT * FROM domains
WHERE id = $1;

-- name: GetClientDomain :one
SELECT d.* FROM domains d
JOIN clients c ON c.domain_id = d.id
WHERE c.id = $1;

-- name: ListClientIDsByDomain :many
SELECT id FROM clients
WHERE domain_id = $1 AND NOT revoked;

-- name: UpdateDomainTLS :one
UPDATE domains
SET tls_enabled      = $2,
    cert_mode        = $3,
    acme_directory   = $4,
    acme_email       = $5,
    acme_credentials = COALESCE(NULLIF(sqlc.arg(acme_credentials)::text, ''), acme_credentials),
    updated_at       = now()
WHERE id = $1
RETURNING *;

-- name: SetDomainAccountKey :exec
UPDATE domains
SET acme_account_key = $2,
    updated_at       = now()
WHERE id = $1;

-- name: UpdateDomainCert :one
UPDATE domains
SET cert_object_key  = $2,
    key_object_key   = $3,
    cert_fingerprint = $4,
    cert_not_after   = $5,
    cert_issued_at   = now(),
    cert_error       = '',
    updated_at       = now()
WHERE id = $1
RETURNING *;

-- name: SetDomainCertError :exec
UPDATE domains
SET cert_error = $2,
    updated_at = now()
WHERE id = $1;

-- name: ClearDomainCert :one
UPDATE domains
SET cert_object_key  = '',
    key_object_key   = '',
    cert_fingerprint = '',
    cert_not_after   = NULL,
    cert_issued_at   = NULL,
    cert_error       = '',
    tls_enabled      = false,
    updated_at       = now()
WHERE id = $1
RETURNING *;

-- name: ListDomainsDueForRenewal :many
SELECT * FROM domains
WHERE tls_enabled
  AND cert_mode = 'acme_cloudflare'
  AND acme_credentials <> ''
  AND (cert_not_after IS NULL OR cert_not_after < now() + make_interval(secs => sqlc.arg(within_seconds)::float))
ORDER BY cert_not_after NULLS FIRST;

-- name: GetSoleDomain :one
SELECT * FROM domains
WHERE (SELECT count(*) FROM domains) = 1;
