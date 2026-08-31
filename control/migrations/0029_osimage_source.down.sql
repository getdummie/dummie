DROP INDEX IF EXISTS osimages_status_idx;

DELETE FROM osimages WHERE source = 'oci';

ALTER TABLE osimages
    DROP CONSTRAINT IF EXISTS osimages_source_ref,
    DROP COLUMN IF EXISTS source,
    DROP COLUMN IF EXISTS oci_ref,
    DROP COLUMN IF EXISTS oci_digest,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS status_detail,
    DROP COLUMN IF EXISTS config_user,
    DROP COLUMN IF EXISTS config_entrypoint,
    DROP COLUMN IF EXISTS config_cmd,
    DROP COLUMN IF EXISTS config_env,
    DROP COLUMN IF EXISTS config_exposed_ports,
    DROP COLUMN IF EXISTS default_port;

DROP INDEX IF EXISTS osimages_object_key_idx;
ALTER TABLE osimages ADD CONSTRAINT osimages_object_key_key UNIQUE (object_key);

ALTER TABLE osimages ALTER COLUMN object_key DROP DEFAULT;
ALTER TABLE osimages ALTER COLUMN file_name DROP DEFAULT;
ALTER TABLE osimages ALTER COLUMN size_bytes DROP DEFAULT;
ALTER TABLE osimages DROP CONSTRAINT osimages_size_bytes_check;
ALTER TABLE osimages ADD CONSTRAINT osimages_size_bytes_check CHECK (size_bytes > 0);

CREATE OR REPLACE FUNCTION osimages_forbid_update() RETURNS trigger AS $$
BEGIN
    IF OLD.soft_deleted_at IS NOT NULL THEN
        RAISE EXCEPTION 'a withdrawn os image cannot be changed';
    END IF;
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.name IS DISTINCT FROM OLD.name
        OR NEW.object_key IS DISTINCT FROM OLD.object_key
        OR NEW.file_name IS DISTINCT FROM OLD.file_name
        OR NEW.size_bytes IS DISTINCT FROM OLD.size_bytes
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
    THEN
        RAISE EXCEPTION 'only the description of an os image can be changed';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
