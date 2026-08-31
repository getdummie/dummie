-- An os image built from a container reference can now be added by a normal
-- user, not only by an admin. The catalogue stays shared -- every user sees
-- every ready image -- so created_by is attribution and permission to withdraw,
-- not visibility. A curated image added by an admin keeps a null owner.
ALTER TABLE osimages ADD COLUMN created_by UUID REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX osimages_owner_idx ON osimages (created_by) WHERE soft_deleted_at IS NULL;

CREATE OR REPLACE FUNCTION osimages_forbid_update() RETURNS trigger AS $$
BEGIN
    -- Deleting a user nulls created_by through the foreign key. Nothing about
    -- the image itself changes, so it has to pass even on a withdrawn or built
    -- row that every rule below would otherwise reject.
    IF NEW.created_by IS DISTINCT FROM OLD.created_by
        AND to_jsonb(NEW.*) - 'created_by' = to_jsonb(OLD.*) - 'created_by'
    THEN
        RETURN NEW;
    END IF;

    IF OLD.soft_deleted_at IS NOT NULL THEN
        RAISE EXCEPTION 'a withdrawn os image cannot be changed';
    END IF;
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.name IS DISTINCT FROM OLD.name
        OR NEW.source IS DISTINCT FROM OLD.source
        OR NEW.oci_ref IS DISTINCT FROM OLD.oci_ref
        OR NEW.created_by IS DISTINCT FROM OLD.created_by
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
    THEN
        RAISE EXCEPTION 'the identity and source of an os image cannot be changed';
    END IF;
    IF OLD.status = 'ready' THEN
        IF NEW.object_key IS DISTINCT FROM OLD.object_key
            OR NEW.file_name IS DISTINCT FROM OLD.file_name
            OR NEW.size_bytes IS DISTINCT FROM OLD.size_bytes
            OR NEW.oci_digest IS DISTINCT FROM OLD.oci_digest
            OR NEW.status IS DISTINCT FROM OLD.status
        THEN
            RAISE EXCEPTION 'the file and the container image of a built os image cannot be changed';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
