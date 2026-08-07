-- name: ListVMNetworkTargets :many
SELECT * FROM vm_network_targets
WHERE vm_id = $1
ORDER BY kind, destination, transport, ports;

-- name: CreateVMNetworkTarget :one
INSERT INTO vm_network_targets (vm_id, destination, kind, transport, ports, note)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: DeleteVMNetworkTarget :exec
-- Scoped by vm_id as well as id: the caller has already proved it owns the VM,
-- so pinning the delete to that VM means a target id from someone else's VM
-- matches nothing rather than deleting their row.
DELETE FROM vm_network_targets
WHERE id = $1 AND vm_id = $2;
