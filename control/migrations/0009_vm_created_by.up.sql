-- 0009_vm_created_by: who asked for this VM.
--
-- Nullable, and that is not a gap to be filled later: a VM adopted from an
-- client's inventory report was created on the host with `dclient vm create` and
-- has no user behind it at all. NULL means unowned, which is a real answer.
-- Rows that predate this column are unowned for the same reason -- the server
-- genuinely does not know.
--
-- ON DELETE SET NULL rather than CASCADE: deleting an account must not delete
-- the record of machines it started, least of all silently.
ALTER TABLE vms ADD COLUMN created_by UUID REFERENCES users(id) ON DELETE SET NULL;

-- The user-facing list filters on this on every page load.
CREATE INDEX idx_vms_created_by ON vms (created_by);
