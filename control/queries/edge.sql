-- name: GetHostEdge :one
SELECT e.* FROM clients h
JOIN domains d ON d.id = h.domain_id
JOIN clients e ON e.id = d.edge_client_id
WHERE h.id = $1
  AND h.role = 'host'
  AND e.role = 'edge'
  AND NOT e.revoked;

-- name: SetDomainEdge :one
UPDATE domains
SET edge_client_id = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ListEdgeHosts :many
-- Online hosts first, so the edge tries a live one before a stale one.
SELECT c.id, d.tld,
       COALESCE(NULLIF(c.advertise_ip, ''), c.reported_ip)::text AS address
FROM clients c
JOIN domains d ON d.id = c.domain_id
WHERE d.edge_client_id = $1
  AND c.role = 'host'
  AND NOT c.revoked
ORDER BY d.tld, (c.status = 'online') DESC, c.last_seen_at DESC NULLS LAST;

-- name: ListEdgeHTTPRoutes :many
SELECT v.name AS vm_name, d.tld AS domain_tld,
       COALESCE(NULLIF(c.advertise_ip, ''), c.reported_ip)::text AS address
FROM vms v
JOIN clients c ON c.id = v.client_id
JOIN domains d ON d.id = c.domain_id
WHERE d.edge_client_id = $1
  AND c.role = 'host'
  AND NOT c.revoked
  AND v.ip <> ''
  AND v.status <> 'gone'
ORDER BY v.created_at, v.vm_id;

-- name: ListEdgeCustomDomainRoutes :many
-- Every status but failed: a name still being issued has to reach its host,
-- which answers the certificate authority's http challenge.
SELECT cd.domain,
       COALESCE(NULLIF(c.advertise_ip, ''), c.reported_ip)::text AS address
FROM vm_custom_domains cd
JOIN vms v ON v.id = cd.vm_id
JOIN clients c ON c.id = v.client_id
JOIN domains d ON d.id = c.domain_id
WHERE d.edge_client_id = $1
  AND c.role = 'host'
  AND NOT c.revoked
  AND cd.status <> 'failed'
  AND v.status <> 'gone'
ORDER BY cd.domain;

-- name: ListEdgeSSHUsers :many
SELECT v.name AS vm_name, u.public_key,
       COALESCE(NULLIF(c.advertise_ip, ''), c.reported_ip)::text AS address
FROM vms v
JOIN users u ON u.id = v.created_by
JOIN clients c ON c.id = v.client_id
JOIN domains d ON d.id = c.domain_id
WHERE d.edge_client_id = $1
  AND c.role = 'host'
  AND NOT c.revoked
  AND v.ip <> ''
  AND v.status <> 'gone'
  AND u.public_key <> ''
ORDER BY v.created_at, v.vm_id;
