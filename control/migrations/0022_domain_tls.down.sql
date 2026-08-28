
DROP INDEX IF EXISTS idx_domains_renewal;

ALTER TABLE domains
    DROP COLUMN tls_enabled,
    DROP COLUMN cert_mode,
    DROP COLUMN acme_directory,
    DROP COLUMN acme_email,
    DROP COLUMN acme_credentials,
    DROP COLUMN acme_account_key,
    DROP COLUMN cert_object_key,
    DROP COLUMN key_object_key,
    DROP COLUMN cert_fingerprint,
    DROP COLUMN cert_not_after,
    DROP COLUMN cert_error,
    DROP COLUMN cert_issued_at,
    DROP COLUMN updated_at;
