ALTER TABLE domains DROP COLUMN edge_client_id;

ALTER TABLE clients
    DROP COLUMN advertise_ip,
    DROP COLUMN reported_ip,
    DROP COLUMN role;
