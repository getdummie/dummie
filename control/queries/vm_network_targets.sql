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

-- name: GetVMNetworkTargetForExpiry :one
-- GetVMNetworkTargetForExpiry reads a target by id alone, with the host of the VM
-- that owns it. Unscoped by owner unlike every other route's read, because the
-- caller is the task runner: an expiry was authorized when it was scheduled, and
-- there is no user on the request to check it against.
--
-- The client id comes back in the same statement because it is what the ruleset
-- and Corefile are regenerated for, and after the delete there is nothing left to
-- look it up from.
SELECT t.id, t.vm_id, t.destination, t.kind, t.transport, t.ports, t.note,
       v.client_id, v.name AS vm_name
FROM vm_network_targets t
JOIN vms v ON v.id = t.vm_id
WHERE t.id = $1;

-- name: DeleteVMNetworkTargetByID :execrows
-- DeleteVMNetworkTargetByID is the expiry's delete. By id alone, for the same
-- reason the read above is: there is no owner on the request to scope it to.
-- The row count distinguishes "expired it" from "somebody already removed it",
-- which is the difference between a done task and a cancelled one.
DELETE FROM vm_network_targets WHERE id = $1;

-- name: ListVMNetworkTargetsByClient :many
-- ListVMNetworkTargetsByClient is the input to the rule generator and the
-- Corefile generator: every allowance on the host, carrying the address of the
-- VM that owns it.
--
-- One query rather than a VM list plus a lookup per VM, because both files are
-- generated as a whole and a half-read fleet would compile to a ruleset that
-- silently revokes whatever was missed.
--
-- Rows without an address are skipped: a rule is scoped to a VM by its source
-- IP, so a VM that has not been given one yet cannot be expressed. It is still
-- covered -- by the default deny, which is what an unscopable allowance should
-- fall back to. 'gone' VMs are excluded for the same reason in reverse: their
-- address will be handed to some other guest, and a stale pass rule would let
-- that guest out.
--
-- The join is a LEFT JOIN, so a VM with no allowances at all still comes back --
-- as one row with an empty destination. That VM is the one the generator most
-- needs to know about: with nothing to allow it has no rule that depends on
-- seeing a handshake, so it gets a blanket deny instead of the split floor, and
-- a VM the query omitted would silently keep the syn on 80/443.
--
-- COALESCE rather than nullable columns. The schema already writes '' for a
-- field that does not apply to a row's kind (see 0010_vm_network_targets), so
-- every consumer reads a string without checking a flag; letting the join
-- introduce NULLs here would be a second way to spell "no value" that only this
-- one query has.
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
ORDER BY v.ip, kind, destination, transport, ports;
