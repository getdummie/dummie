ALTER TABLE clients
    ADD COLUMN role         TEXT NOT NULL DEFAULT 'host' CHECK (role IN ('host', 'edge')),
    ADD COLUMN reported_ip  TEXT NOT NULL DEFAULT '',
    ADD COLUMN advertise_ip TEXT NOT NULL DEFAULT '';

ALTER TABLE domains
    ADD COLUMN edge_client_id UUID REFERENCES clients(id) ON DELETE SET NULL;
