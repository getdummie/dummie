-- Back to a fully immutable row: the description joins the columns nothing may
-- change once saved.
CREATE OR REPLACE FUNCTION kernels_forbid_update() RETURNS trigger AS $$
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
