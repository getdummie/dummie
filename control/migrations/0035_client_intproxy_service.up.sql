-- The rest of the per-service columns intproxy needs, matching the shape every
-- other managed service has: a pinned version, an override url, and what the
-- host reports it actually has installed.
--
-- IF NOT EXISTS because some databases had these added by hand while 0034 was
-- being corrected, and this has to be a no-op there rather than a failure.
ALTER TABLE clients
    ADD COLUMN IF NOT EXISTS intproxy_download_url      TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS intproxy_installed_version TEXT NOT NULL DEFAULT '';
