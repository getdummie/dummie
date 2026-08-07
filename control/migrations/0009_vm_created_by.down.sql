DROP INDEX IF EXISTS idx_vms_created_by;
ALTER TABLE vms DROP COLUMN IF EXISTS created_by;
