ALTER TABLE vms DROP COLUMN public_ports;
ALTER TABLE vms DROP COLUMN default_port;
ALTER TABLE vms DROP CONSTRAINT vms_name_shape;
ALTER TABLE vms DROP CONSTRAINT vms_name_key;
