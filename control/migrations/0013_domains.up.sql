-- 0013_domains: the set of DNS suffixes this installation owns, and the one an
-- agent belongs to.

CREATE TABLE domains (
    id  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tld TEXT NOT NULL UNIQUE
);

-- SET NULL rather than RESTRICT: removing a domain is an operator decision about
-- the domain, and it should not be blocked by, or take down with it, the agents
-- that happen to point at it.
ALTER TABLE agents
    ADD COLUMN domain_id UUID REFERENCES domains(id) ON DELETE SET NULL;
