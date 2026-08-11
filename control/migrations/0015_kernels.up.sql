-- 0015_kernels: the catalogue of kernel images an installation can boot from.
--
-- A row is a name plus a blob in object storage. Both are written once, at
-- upload, and are never edited afterwards: the point of a catalogue entry is
-- that what someone booted last month is still what that name means today.
-- Withdrawing an entry is a soft delete, so the record of what a name meant
-- survives even after it stops being offered.

CREATE TABLE kernels (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Unique because the name is how an operator picks a kernel; two rows
    -- answering to one name would make that choice meaningless. Uniqueness
    -- spans soft-deleted rows too -- a withdrawn name is not free to reuse,
    -- or the same name would refer to two different images over time.
    name        TEXT NOT NULL UNIQUE CHECK (char_length(name) BETWEEN 1 AND 128),
    description TEXT NOT NULL DEFAULT '',
    -- The object in the configured bucket, not a URL: the bucket is private, so
    -- what a caller downloads is a presigned link minted per request. Storing a
    -- URL would bake in today's endpoint and today's expiry.
    object_key  TEXT NOT NULL UNIQUE,
    -- What the admin uploaded it as, and how big it was. Kept so the detail page
    -- and the download hand-off have something to show and to name the file.
    file_name   TEXT NOT NULL,
    size_bytes  BIGINT NOT NULL CHECK (size_bytes > 0),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- NULL while the kernel is offered. Set when it is withdrawn; the row and
    -- the object both stay.
    soft_deleted_at TIMESTAMPTZ
);

CREATE INDEX kernels_live_idx ON kernels (created_at DESC) WHERE soft_deleted_at IS NULL;

-- Immutability enforced here rather than only in the API: "not even an admin"
-- has to mean something a future handler, a migration or a psql session cannot
-- quietly undo. Withdrawing is the one permitted change, so soft_deleted_at is
-- the one column this lets through -- and only from NULL, so a withdrawal
-- cannot be reversed either.
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
