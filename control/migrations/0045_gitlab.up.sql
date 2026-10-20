-- GitLab instances an admin has made reachable through intproxy. The name is
-- the subdomain, <name>.int.<tld>, so it is immutable once created.
CREATE TABLE gitlab_instances (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL UNIQUE,
    base_url   TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    CONSTRAINT gitlab_instances_name_shape CHECK (
        char_length(name) BETWEEN 2 AND 63
        AND name ~ '^[a-z0-9]+(-[a-z0-9]+)*$'
        AND name NOT IN ('github', 'llm')
    )
);

ALTER TABLE integrations DROP CONSTRAINT integrations_kind_check;
ALTER TABLE integrations ADD CONSTRAINT integrations_kind_check CHECK (kind IN ('github', 'gitlab'));

-- A personal access token, sealed like llm keys: the broker has to hand the
-- plaintext to intproxy, so it cannot be hashed.
ALTER TABLE integrations
    ADD COLUMN gitlab_instance_id UUID REFERENCES gitlab_instances(id) ON DELETE CASCADE,
    ADD COLUMN token_enc          BYTEA,
    ADD COLUMN gitlab_username    TEXT NOT NULL DEFAULT '';

ALTER TABLE integrations ADD CONSTRAINT integrations_gitlab_shape CHECK (
    kind <> 'gitlab' OR (gitlab_instance_id IS NOT NULL AND token_enc IS NOT NULL)
);
