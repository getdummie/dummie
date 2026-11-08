-- A saved set of create-vm choices. config is the create form, every field
-- optional; ids in it may outlive what they name and are checked at launch.
CREATE TABLE recipes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
    description TEXT NOT NULL DEFAULT '',
    config      JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner_id, name)
);

-- So a vm can be saved back as a recipe. Null for vms created before this.
ALTER TABLE vms ADD COLUMN osimage_id UUID REFERENCES osimages(id) ON DELETE SET NULL;
