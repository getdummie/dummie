ALTER TABLE vms DROP CONSTRAINT vms_status_check;
ALTER TABLE vms ADD CONSTRAINT vms_status_check
    CHECK (status IN ('pending', 'running', 'stopped', 'failed', 'gone'));
