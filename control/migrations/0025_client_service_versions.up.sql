ALTER TABLE clients
    ADD COLUMN dclient_version      TEXT NOT NULL DEFAULT '',
    ADD COLUMN dclient_download_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN dpipe_version        TEXT NOT NULL DEFAULT '',
    ADD COLUMN dpipe_download_url   TEXT NOT NULL DEFAULT '',
    ADD COLUMN proxy_version        TEXT NOT NULL DEFAULT '',
    ADD COLUMN proxy_download_url   TEXT NOT NULL DEFAULT '';

ALTER TABLE clients
    ADD COLUMN dpipe_installed_version TEXT NOT NULL DEFAULT '',
    ADD COLUMN proxy_installed_version TEXT NOT NULL DEFAULT '';
