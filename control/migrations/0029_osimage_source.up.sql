ALTER TABLE osimages
    ADD COLUMN source        TEXT NOT NULL DEFAULT 'upload'
        CHECK (source IN ('upload', 'oci')),
    ADD COLUMN oci_ref       TEXT NOT NULL DEFAULT '',
    ADD COLUMN oci_digest    TEXT NOT NULL DEFAULT '',
    ADD COLUMN status        TEXT NOT NULL DEFAULT 'ready'
        CHECK (status IN ('pending', 'building', 'ready', 'failed')),
    ADD COLUMN status_detail TEXT NOT NULL DEFAULT '',
    ADD COLUMN config_user          TEXT NOT NULL DEFAULT '',
    ADD COLUMN config_entrypoint    TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN config_cmd           TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN config_env           TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN config_exposed_ports INTEGER[] NOT NULL DEFAULT '{}',
    ADD COLUMN default_port  INTEGER
        CHECK (default_port IS NULL OR default_port BETWEEN 1 AND 65535);

ALTER TABLE osimages
    ADD CONSTRAINT osimages_source_ref CHECK ((source = 'oci') = (oci_ref <> ''));

-- An oci image is inserted before its tar exists, so the row has to be allowed
-- through with no object and no size until the build fills them in.
ALTER TABLE osimages DROP CONSTRAINT osimages_size_bytes_check;
ALTER TABLE osimages ADD CONSTRAINT osimages_size_bytes_check CHECK (size_bytes >= 0);
ALTER TABLE osimages ALTER COLUMN object_key SET DEFAULT '';
ALTER TABLE osimages ALTER COLUMN file_name SET DEFAULT '';
ALTER TABLE osimages ALTER COLUMN size_bytes SET DEFAULT 0;

ALTER TABLE osimages DROP CONSTRAINT osimages_object_key_key;
CREATE UNIQUE INDEX osimages_object_key_idx ON osimages (object_key) WHERE object_key <> '';

CREATE OR REPLACE FUNCTION osimages_forbid_update() RETURNS trigger AS $$
BEGIN
    IF OLD.soft_deleted_at IS NOT NULL THEN
        RAISE EXCEPTION 'a withdrawn os image cannot be changed';
    END IF;
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.name IS DISTINCT FROM OLD.name
        OR NEW.source IS DISTINCT FROM OLD.source
        OR NEW.oci_ref IS DISTINCT FROM OLD.oci_ref
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
    THEN
        RAISE EXCEPTION 'the identity and source of an os image cannot be changed';
    END IF;
    -- Everything else stays open until the build settles: a pending, building or
    -- failed row is still on its way to becoming an image, and a retry has to be
    -- able to write over what the last attempt left behind.
    IF OLD.status = 'ready' THEN
        IF NEW.object_key IS DISTINCT FROM OLD.object_key
            OR NEW.file_name IS DISTINCT FROM OLD.file_name
            OR NEW.size_bytes IS DISTINCT FROM OLD.size_bytes
            OR NEW.oci_digest IS DISTINCT FROM OLD.oci_digest
            OR NEW.status IS DISTINCT FROM OLD.status
            OR NEW.config_user IS DISTINCT FROM OLD.config_user
            OR NEW.config_entrypoint IS DISTINCT FROM OLD.config_entrypoint
            OR NEW.config_cmd IS DISTINCT FROM OLD.config_cmd
            OR NEW.config_env IS DISTINCT FROM OLD.config_env
            OR NEW.config_exposed_ports IS DISTINCT FROM OLD.config_exposed_ports
        THEN
            RAISE EXCEPTION 'only the description and default port of a built os image can be changed';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE INDEX osimages_status_idx ON osimages (status) WHERE soft_deleted_at IS NULL;
