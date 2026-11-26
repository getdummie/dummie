-- name: ListVMNetworkTargets :many
SELECT * FROM vm_network_targets
WHERE vm_id = $1
ORDER BY kind, destination, transport, ports;

-- name: CreateVMNetworkTarget :one
INSERT INTO vm_network_targets (vm_id, destination, kind, transport, ports, note)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: DeleteVMNetworkTarget :exec
DELETE FROM vm_network_targets
WHERE id = $1 AND vm_id = $2;

-- name: GetVMNetworkTargetForExpiry :one
SELECT t.id, t.vm_id, t.destination, t.kind, t.transport, t.ports, t.note,
       v.client_id, v.name AS vm_name
FROM vm_network_targets t
JOIN vms v ON v.id = t.vm_id
WHERE t.id = $1;

-- name: DeleteVMNetworkTargetByID :execrows
DELETE FROM vm_network_targets WHERE id = $1;

-- name: ListVMNetworkTargetsByClient :many
SELECT v.ip AS vm_ip, v.vm_id AS host_vm_id, v.name AS vm_name,
       COALESCE(t.kind, '') AS kind,
       COALESCE(t.destination, '') AS destination,
       COALESCE(t.transport, '') AS transport,
       COALESCE(t.ports, '') AS ports,
       COALESCE(t.note, '') AS note
FROM vms v
LEFT JOIN vm_network_targets t ON t.vm_id = v.id
WHERE v.client_id = $1
  AND v.ip <> ''
  AND v.status <> 'gone'
UNION ALL
SELECT v.ip, v.vm_id, v.name, e.kind, e.destination, e.transport, e.ports,
       'group ' || g.name || CASE WHEN e.note <> '' THEN ': ' || e.note ELSE '' END
FROM vms v
JOIN vm_destination_groups a ON a.vm_id = v.id
JOIN destination_groups g ON g.id = a.group_id
JOIN destination_group_entries e ON e.group_id = g.id
WHERE v.client_id = $1
  AND v.ip <> ''
  AND v.status <> 'gone'
ORDER BY vm_ip, kind, destination, transport, ports;
