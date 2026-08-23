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
-- GetClientDomain returns the domain of one client, and no rows when the client
-- has none. The push paths need the whole row rather than the tld, because what
-- they are deciding is whether to turn tls on for that host.
SELECT d.* FROM domains d
JOIN clients c ON c.domain_id = d.id
WHERE c.id = $1;

-- name: ListClientIDsByDomain :many
-- Who to push to when a domain's certificate changes.
SELECT id FROM clients
WHERE domain_id = $1 AND NOT revoked;

-- name: UpdateDomainTLS :one
-- The operator-editable half. acme_credentials keeps its stored value when the
-- argument is empty, so saving the form without retyping a token does not wipe
-- it -- which also means the only way to remove one is to change provider.
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
-- Written once, the first time a domain talks to a ca.
UPDATE domains
SET acme_account_key = $2,
    updated_at       = now()
WHERE id = $1;

-- name: UpdateDomainCert :one
-- One statement for a successful issuance: where the certificate is, what it is,
-- and the clearing of whatever error the last attempt left behind.
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
-- A failed attempt records why and changes nothing else: the certificate already
-- in place stays in place and stays served.
UPDATE domains
SET cert_error = $2,
    updated_at = now()
WHERE id = $1;

-- name: ClearDomainCert :one
-- Forgets the certificate and turns tls off in the same statement, because a
-- host left with tls on and nothing to serve it with is a dpipe that will not
-- start.
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
-- What the daily renewal task acts on. Only the automated mode: an uploaded or
-- hand-walked certificate cannot be replaced without a person, so listing one
-- here would produce a failure every day instead of a warning once.
SELECT * FROM domains
WHERE tls_enabled
  AND cert_mode = 'acme_cloudflare'
  AND acme_credentials <> ''
  AND (cert_not_after IS NULL OR cert_not_after < now() + make_interval(secs => sqlc.arg(within_seconds)::float))
ORDER BY cert_not_after NULLS FIRST;

-- name: GetSoleDomain :one
-- GetSoleDomain returns the single configured domain, and no rows when there is
-- none or more than one. Enrollment uses it to pick a domain for a new client
-- without having to guess: with exactly one there is nothing to choose, and with
-- several the choice is the operator's, so the client is left unassigned.
SELECT * FROM domains
WHERE (SELECT count(*) FROM domains) = 1;
