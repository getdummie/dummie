DELETE FROM integrations WHERE kind = 'gitlab';

ALTER TABLE integrations DROP CONSTRAINT IF EXISTS integrations_gitlab_shape;
ALTER TABLE integrations
    DROP COLUMN IF EXISTS gitlab_username,
    DROP COLUMN IF EXISTS token_enc,
    DROP COLUMN IF EXISTS gitlab_instance_id;

ALTER TABLE integrations DROP CONSTRAINT integrations_kind_check;
ALTER TABLE integrations ADD CONSTRAINT integrations_kind_check CHECK (kind IN ('github'));

DROP TABLE IF EXISTS gitlab_instances;
