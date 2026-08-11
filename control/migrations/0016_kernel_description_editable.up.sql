-- 0016_kernel_description_editable: the description becomes the one editable
-- field on a kernel.
--
-- What makes an entry trustworthy is that the name and the image behind it never
-- change. A description is a note about them, not part of their identity, and
-- being unable to correct a typo in one was a cost with nothing bought by it.
--
-- A withdrawn kernel is closed to every change, including this one: it is a
-- record of what a name used to mean, and editing it would rewrite that record.

CREATE OR REPLACE FUNCTION kernels_forbid_update() RETURNS trigger AS $$
BEGIN
    IF OLD.soft_deleted_at IS NOT NULL THEN
        RAISE EXCEPTION 'a withdrawn kernel cannot be changed';
    END IF;
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.name IS DISTINCT FROM OLD.name
        OR NEW.object_key IS DISTINCT FROM OLD.object_key
        OR NEW.file_name IS DISTINCT FROM OLD.file_name
        OR NEW.size_bytes IS DISTINCT FROM OLD.size_bytes
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
    THEN
        RAISE EXCEPTION 'only the description of a kernel can be changed';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
