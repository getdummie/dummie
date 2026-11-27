CREATE TABLE workspace_layouts (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
    layout     JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner_id, name)
);

-- No row means the vm opens with the built-in default layout.
CREATE TABLE vm_workspace_layouts (
    vm_id     UUID PRIMARY KEY REFERENCES vms(id) ON DELETE CASCADE,
    layout_id UUID NOT NULL REFERENCES workspace_layouts(id) ON DELETE CASCADE
);

CREATE INDEX idx_vm_workspace_layouts_layout_id ON vm_workspace_layouts (layout_id);
