DROP INDEX idx_vm_custom_domains_vm;

ALTER TABLE vm_custom_domains ADD CONSTRAINT vm_custom_domains_vm_id_key UNIQUE (vm_id);
