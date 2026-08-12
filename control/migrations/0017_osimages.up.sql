-- 0017_osimages: the catalogue of OS images an installation can build a guest
-- from. The kernels table's shape and rules, for the other half of a direct boot.
--
-- A row is a name plus a blob in object storage. The name and the image are
-- written once: what someone built from a name last month is still what that
-- name means today. The description is a note about them and can be corrected.
-- Withdrawing an entry is a soft delete, so the record of what a name meant
-- survives even after it stops being offered.

CREATE TABLE osimages (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Unique because the name is how an operator picks an image; two rows
    -- answering to one name would make that choice meaningless. Uniqueness
    -- spans soft-deleted rows too -- a withdrawn name is not free to reuse,
    -- or the same name would refer to two different images over time.
    name        TEXT NOT NULL UNIQUE CHECK (char_length(name) BETWEEN 1 AND 128),
    description TEXT NOT NULL DEFAULT '',
    -- The object in the configured bucket, not a URL: the bucket is private, so
    -- what a caller downloads is a presigned link minted per request. Storing a
    -- URL would bake in today's endpoint and today's expiry.
    object_key  TEXT NOT NULL UNIQUE,
    file_name   TEXT NOT NULL,
    size_bytes  BIGINT NOT NULL CHECK (size_bytes > 0),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- NULL while the image is offered. Set when it is withdrawn; the row and
    -- the object both stay.
    soft_deleted_at TIMESTAMPTZ
);

CREATE INDEX osimages_live_idx ON osimages (created_at DESC) WHERE soft_deleted_at IS NULL;

-- Immutability enforced here rather than only in the API, for the same reason it
-- is on kernels: "not even an admin" has to mean something a future handler, a
-- migration or a psql session cannot quietly undo.
CREATE FUNCTION osimages_forbid_update() RETURNS trigger AS $$
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

CREATE TRIGGER osimages_no_edit
    BEFORE UPDATE ON osimages
    FOR EACH ROW EXECUTE FUNCTION osimages_forbid_update();
