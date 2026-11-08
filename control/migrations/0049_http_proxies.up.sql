-- User-configured http proxies, served at <name>-u.int.<tld>. The name is the
-- hostname, so it is unique across every user, not just per owner.
ALTER TABLE integrations DROP CONSTRAINT integrations_kind_check;
ALTER TABLE integrations ADD CONSTRAINT integrations_kind_check CHECK (kind IN ('github', 'gitlab', 'http'));

-- Headers are sealed as one JSON list: they routinely carry tokens, and the
-- broker has to hand the plaintext to intproxy.
ALTER TABLE integrations
    ADD COLUMN proxy_target_url  TEXT    NOT NULL DEFAULT '',
    ADD COLUMN proxy_methods     TEXT[]  NOT NULL DEFAULT '{}',
    ADD COLUMN proxy_verify_tls  BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN proxy_headers_enc BYTEA;

ALTER TABLE integrations ADD CONSTRAINT integrations_http_shape CHECK (
    kind <> 'http' OR (
        proxy_target_url <> ''
        AND cardinality(proxy_methods) > 0
        AND proxy_methods <@ ARRAY['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS']
    )
);

CREATE UNIQUE INDEX integrations_http_name ON integrations (name) WHERE kind = 'http';

-- -u is the user proxies' suffix, so an instance must never end in it.
ALTER TABLE gitlab_instances DROP CONSTRAINT gitlab_instances_name_shape;
ALTER TABLE gitlab_instances ADD CONSTRAINT gitlab_instances_name_shape CHECK (
    char_length(name) BETWEEN 2 AND 63
    AND name ~ '^[a-z0-9]+(-[a-z0-9]+)*$'
    AND name NOT IN ('github', 'llm', 'u')
    AND name !~ '-u$'
);
