-- name: CreateHTTPProxyIntegration :one
-- The id comes from the caller, since the sealed headers are bound to it.
INSERT INTO integrations (id, owner_id, name, kind, all_repos, proxy_target_url, proxy_methods, proxy_verify_tls, proxy_headers_enc)
VALUES ($1, $2, $3, 'http', true, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateHTTPProxyIntegration :execrows
UPDATE integrations
SET proxy_target_url  = $3,
    proxy_methods     = $4,
    proxy_verify_tls  = $5,
    proxy_headers_enc = $6,
    updated_at        = now()
WHERE id = $1 AND owner_id = $2 AND kind = 'http';

-- name: GetHTTPProxyGrantForVM :one
-- The broker's authorization query for a user proxy, with the same
-- load-bearing predicate as the others: client_id comes from the calling
-- host's own token, never from the request.
SELECT i.id            AS integration_id,
       i.name          AS integration_name,
       i.owner_id,
       i.proxy_target_url,
       i.proxy_methods,
       i.proxy_verify_tls,
       i.proxy_headers_enc,
       v.name          AS vm_name
FROM vms v
JOIN vm_integrations vi ON vi.vm_id = v.id
JOIN integrations i ON i.id = vi.integration_id AND i.kind = 'http'
WHERE v.client_id = sqlc.arg(client_id)
  AND v.ip = sqlc.arg(vm_ip)
  AND i.name = sqlc.arg(proxy_name)
  AND i.owner_id = v.created_by;
