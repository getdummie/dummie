ALTER TABLE gitlab_instances DROP CONSTRAINT gitlab_instances_name_shape;
ALTER TABLE gitlab_instances ADD CONSTRAINT gitlab_instances_name_shape CHECK (
    char_length(name) BETWEEN 2 AND 63
    AND name ~ '^[a-z0-9]+(-[a-z0-9]+)*$'
    AND name NOT IN ('github', 'llm')
);

DELETE FROM integrations WHERE kind = 'http';

DROP INDEX integrations_http_name;
ALTER TABLE integrations DROP CONSTRAINT integrations_http_shape;
ALTER TABLE integrations
    DROP COLUMN proxy_target_url,
    DROP COLUMN proxy_methods,
    DROP COLUMN proxy_verify_tls,
    DROP COLUMN proxy_headers_enc;

ALTER TABLE integrations DROP CONSTRAINT integrations_kind_check;
ALTER TABLE integrations ADD CONSTRAINT integrations_kind_check CHECK (kind IN ('github', 'gitlab'));
