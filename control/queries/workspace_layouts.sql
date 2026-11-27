-- name: CreateWorkspaceLayout :one
INSERT INTO workspace_layouts (owner_id, name, layout)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListWorkspaceLayoutsByOwner :many
SELECT * FROM workspace_layouts
WHERE owner_id = $1
ORDER BY name;

-- name: GetWorkspaceLayoutForOwner :one
SELECT * FROM workspace_layouts
WHERE id = $1 AND owner_id = $2;

-- name: UpdateWorkspaceLayoutForOwner :one
UPDATE workspace_layouts
SET name       = $3,
    layout     = $4,
    updated_at = now()
WHERE id = $1 AND owner_id = $2
RETURNING *;

-- name: DeleteWorkspaceLayoutForOwner :execrows
DELETE FROM workspace_layouts
WHERE id = $1 AND owner_id = $2;

-- name: GetVMWorkspaceLayout :one
SELECT * FROM workspace_layouts
WHERE owner_id = $2 AND id = (
    SELECT layout_id FROM vm_workspace_layouts WHERE vm_id = $1
);

-- name: SetVMWorkspaceLayout :exec
INSERT INTO vm_workspace_layouts (vm_id, layout_id)
VALUES ($1, $2)
ON CONFLICT (vm_id) DO UPDATE SET layout_id = EXCLUDED.layout_id;

-- name: ClearVMWorkspaceLayout :exec
DELETE FROM vm_workspace_layouts
WHERE vm_id = $1;
