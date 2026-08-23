-- 0022_domain_tls: everything about the wildcard certificate a domain is served
-- with. It hangs off domains rather than settings because a provider token is a
-- fact about one zone, and an installation with two domains at two registrars
-- has no single answer to give.
--
-- The certificate itself is not here. It lives in object storage like the
-- kernels and os images do; these columns hold where it is and what it is, so a
-- listing can say "expires in 9 days" without reading a bucket.

ALTER TABLE domains
    -- Off by default, and deliberately separate from having a certificate: an
    -- operator uploading one is not the same act as putting the fleet on https,
    -- and the certificate for an internal tld may never be publicly valid at all.
    ADD COLUMN tls_enabled BOOLEAN NOT NULL DEFAULT false,

    -- upload | acme_manual | acme_cloudflare. Not a CHECK, for the same reason
    -- scheduled_tasks.kind is not one: the registry in Go is the list, and a
    -- migration to add a provider would make this the wrong place to keep it.
    ADD COLUMN cert_mode TEXT NOT NULL DEFAULT 'upload',

    -- staging | production. Staging by default so a local run, or a first
    -- attempt at a zone's credentials, cannot spend the production rate limit --
    -- which is per registered domain per week, and is the one mistake here that
    -- takes days rather than minutes to recover from.
    ADD COLUMN acme_directory TEXT NOT NULL DEFAULT 'staging',
    ADD COLUMN acme_email TEXT NOT NULL DEFAULT '',

    -- The dns provider's credential, as json shaped by the provider. Never read
    -- back over the api: it is a token that can rewrite the operator's zone, so
    -- an admin screen that displayed it would turn every open tab into somewhere
    -- it can leak from. Stored in plaintext, like every other secret in this
    -- table -- worth knowing before pointing this at a zone that matters.
    ADD COLUMN acme_credentials TEXT NOT NULL DEFAULT '',

    -- The acme account key, generated on first use and kept for the life of the
    -- domain. Regenerating it on every issuance would register a new account
    -- with the ca each time, which is its own rate limit.
    ADD COLUMN acme_account_key TEXT NOT NULL DEFAULT '',

    ADD COLUMN cert_object_key TEXT NOT NULL DEFAULT '',
    ADD COLUMN key_object_key TEXT NOT NULL DEFAULT '',
    -- sha256 of the leaf. What the push path compares, so an unchanged
    -- certificate never becomes a job.
    ADD COLUMN cert_fingerprint TEXT NOT NULL DEFAULT '',
    ADD COLUMN cert_not_after TIMESTAMPTZ,
    -- Why the last attempt failed, shown against the domain. Empty is not "it
    -- worked" on its own -- a domain that has never been issued has both this
    -- and cert_fingerprint empty.
    ADD COLUMN cert_error TEXT NOT NULL DEFAULT '',
    ADD COLUMN cert_issued_at TIMESTAMPTZ,
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- The renewal task asks for exactly this: automated domains whose certificate
-- runs out soon. Partial, because the answer is a handful of rows out of a table
-- that is already small.
CREATE INDEX idx_domains_renewal
    ON domains (cert_not_after)
    WHERE tls_enabled AND cert_mode = 'acme_cloudflare';
