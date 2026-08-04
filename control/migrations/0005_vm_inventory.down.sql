-- Rows in the statuses being removed would fail the narrower constraint, so they
-- are collapsed into 'failed' first.
UPDATE vms SET status = 'failed' WHERE status IN ('stopped', 'gone');

ALTER TABLE vms DROP CONSTRAINT vms_status_check;
ALTER TABLE vms ADD CONSTRAINT vms_status_check
    CHECK (status IN ('pending', 'running', 'failed'));
