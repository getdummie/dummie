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

-- name: ListVMNetworkTargetsByAgent :many
-- ListVMNetworkTargetsByAgent is the input to the rule generator: every
-- allowance on the host, carrying the address of the VM that owns it.
--
-- One query rather than a VM list plus a lookup per VM, because the file is
-- generated as a whole and a half-read fleet would compile to a ruleset that
-- silently revokes whatever was missed.
--
-- Rows without an address are skipped: a rule is scoped to a VM by its source
-- IP, so a VM that has not been given one yet cannot be expressed. It is still
-- covered -- by the default deny, which is what an unscopable allowance should
-- fall back to. 'gone' VMs are excluded for the same reason in reverse: their
-- address will be handed to some other guest, and a stale pass rule would let
-- that guest out.
SELECT v.ip AS vm_ip, v.vm_id AS host_vm_id, v.name AS vm_name,
       t.kind, t.destination, t.transport, t.ports, t.note
FROM vm_network_targets t
JOIN vms v ON v.id = t.vm_id
WHERE v.agent_id = $1
  AND v.ip <> ''
  AND v.status <> 'gone'
ORDER BY v.ip, t.kind, t.destination, t.transport, t.ports;
