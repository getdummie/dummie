-- name: ListDestinationGroups :many
SELECT g.*,
       (SELECT count(*) FROM vm_destination_groups a
        JOIN vms v ON v.id = a.vm_id
        WHERE a.group_id = g.id AND v.status <> 'gone')::bigint AS vm_count
FROM destination_groups g
ORDER BY g.name;

-- name: GetDestinationGroup :one
SELECT * FROM destination_groups WHERE id = $1;

-- name: ListDestinationGroupNames :many
SELECT name FROM destination_groups;

-- name: CreateDestinationGroup :one
INSERT INTO destination_groups (name, description)
VALUES ($1, $2)
RETURNING *;

-- name: UpdateDestinationGroup :one
UPDATE destination_groups
SET name = $2, description = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteDestinationGroup :execrows
DELETE FROM destination_groups WHERE id = $1;

-- name: ListAllDestinationGroupEntries :many
SELECT * FROM destination_group_entries
ORDER BY group_id, kind, destination, transport, ports;

-- name: CreateDestinationGroupEntry :exec
INSERT INTO destination_group_entries (group_id, kind, destination, transport, ports, note)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: DeleteDestinationGroupEntries :exec
DELETE FROM destination_group_entries WHERE group_id = $1;

-- name: ListClientsForDestinationGroup :many
SELECT DISTINCT v.client_id
FROM vm_destination_groups a
JOIN vms v ON v.id = a.vm_id
WHERE a.group_id = $1 AND v.status <> 'gone';

-- name: AttachDestinationGroupToVM :one
INSERT INTO vm_destination_groups (vm_id, group_id)
VALUES ($1, $2)
RETURNING *;

-- name: ListVMDestinationGroups :many
SELECT a.id, a.group_id, a.created_at, g.name, g.description
FROM vm_destination_groups a
JOIN destination_groups g ON g.id = a.group_id
WHERE a.vm_id = $1
ORDER BY g.name;

-- name: ListDestinationGroupEntriesForVM :many
SELECT e.*
FROM destination_group_entries e
JOIN vm_destination_groups a ON a.group_id = e.group_id
WHERE a.vm_id = $1
ORDER BY e.group_id, e.kind, e.destination, e.transport, e.ports;

-- name: DeleteVMDestinationGroup :execrows
DELETE FROM vm_destination_groups WHERE id = $1 AND vm_id = $2;

-- name: GetVMDestinationGroupForExpiry :one
SELECT a.id, a.vm_id, a.group_id, g.name AS group_name, v.client_id, v.name AS vm_name
FROM vm_destination_groups a
JOIN destination_groups g ON g.id = a.group_id
JOIN vms v ON v.id = a.vm_id
WHERE a.id = $1;

-- name: DeleteVMDestinationGroupByID :execrows
DELETE FROM vm_destination_groups WHERE id = $1;
