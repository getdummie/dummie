ALTER TABLE vm_custom_domains DROP CONSTRAINT vm_custom_domains_vm_id_key;

CREATE INDEX idx_vm_custom_domains_vm ON vm_custom_domains (vm_id);
