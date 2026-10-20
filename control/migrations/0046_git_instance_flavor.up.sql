-- gitlab_instances also holds forgejo and gitea instances. Both speak the same
-- git over https and take a token the same way, so only the token check and
-- the console copy differ. 'gitea' covers forgejo too.
ALTER TABLE gitlab_instances
    ADD COLUMN flavor TEXT NOT NULL DEFAULT 'gitlab' CHECK (flavor IN ('gitlab', 'gitea'));
