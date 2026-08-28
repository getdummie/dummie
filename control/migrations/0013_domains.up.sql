
CREATE TABLE domains (
    id  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tld TEXT NOT NULL UNIQUE
);

ALTER TABLE clients
    ADD COLUMN domain_id UUID REFERENCES domains(id) ON DELETE SET NULL;
