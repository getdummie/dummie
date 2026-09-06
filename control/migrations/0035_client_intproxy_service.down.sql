ALTER TABLE clients
    DROP COLUMN IF EXISTS intproxy_download_url,
    DROP COLUMN IF EXISTS intproxy_installed_version;
