ALTER TABLE vms ADD COLUMN created_by UUID REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX idx_vms_created_by ON vms (created_by);
