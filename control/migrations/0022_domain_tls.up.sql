
ALTER TABLE domains
    ADD COLUMN tls_enabled BOOLEAN NOT NULL DEFAULT false,

    ADD COLUMN cert_mode TEXT NOT NULL DEFAULT 'upload',

    ADD COLUMN acme_directory TEXT NOT NULL DEFAULT 'staging',
    ADD COLUMN acme_email TEXT NOT NULL DEFAULT '',

    ADD COLUMN acme_credentials TEXT NOT NULL DEFAULT '',

    ADD COLUMN acme_account_key TEXT NOT NULL DEFAULT '',

    ADD COLUMN cert_object_key TEXT NOT NULL DEFAULT '',
    ADD COLUMN key_object_key TEXT NOT NULL DEFAULT '',
    ADD COLUMN cert_fingerprint TEXT NOT NULL DEFAULT '',
    ADD COLUMN cert_not_after TIMESTAMPTZ,
    ADD COLUMN cert_error TEXT NOT NULL DEFAULT '',
    ADD COLUMN cert_issued_at TIMESTAMPTZ,
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE INDEX idx_domains_renewal
    ON domains (cert_not_after)
    WHERE tls_enabled AND cert_mode = 'acme_cloudflare';
