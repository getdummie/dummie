ALTER TABLE clients
    ADD COLUMN dinit_version           TEXT NOT NULL DEFAULT '',
    ADD COLUMN dinit_download_url      TEXT NOT NULL DEFAULT '',
    ADD COLUMN dinit_installed_version TEXT NOT NULL DEFAULT '';
