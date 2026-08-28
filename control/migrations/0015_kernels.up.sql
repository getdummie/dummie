
CREATE TABLE kernels (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE CHECK (char_length(name) BETWEEN 1 AND 128),
    description TEXT NOT NULL DEFAULT '',
    object_key  TEXT NOT NULL UNIQUE,
    file_name   TEXT NOT NULL,
    size_bytes  BIGINT NOT NULL CHECK (size_bytes > 0),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    soft_deleted_at TIMESTAMPTZ
);

CREATE INDEX kernels_live_idx ON kernels (created_at DESC) WHERE soft_deleted_at IS NULL;

CREATE FUNCTION kernels_forbid_update() RETURNS trigger AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.name IS DISTINCT FROM OLD.name
        OR NEW.description IS DISTINCT FROM OLD.description
        OR NEW.object_key IS DISTINCT FROM OLD.object_key
        OR NEW.file_name IS DISTINCT FROM OLD.file_name
        OR NEW.size_bytes IS DISTINCT FROM OLD.size_bytes
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
    THEN
        RAISE EXCEPTION 'kernels are immutable once saved';
    END IF;
    IF OLD.soft_deleted_at IS NOT NULL AND NEW.soft_deleted_at IS DISTINCT FROM OLD.soft_deleted_at THEN
        RAISE EXCEPTION 'a withdrawn kernel cannot be changed';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER kernels_no_edit
    BEFORE UPDATE ON kernels
    FOR EACH ROW EXECUTE FUNCTION kernels_forbid_update();
