-- 0023_oidc: federated sign-in against any number of OIDC providers.
--
-- A provider is a row rather than a setting because there can be several of
-- them and each is a small record, not a value: the settings table is one TEXT
-- column keyed by a name the server declares up front, which cannot express
-- "three of these, added at runtime".
--
-- Everything needed to talk to the provider is discovered from the issuer at
-- request time (/.well-known/openid-configuration), so the endpoints are
-- deliberately not columns here -- a provider that moves its token endpoint
-- would otherwise need an admin to notice and edit a row.
CREATE TABLE oidc_providers (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Appears in the callback URL registered with the provider, so it is fixed
    -- once the provider knows it. Constrained here as well as in Go because a
    -- slug with a slash or a space in it would silently break that URL.
    slug          TEXT NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,31}$'),
    display_name  TEXT NOT NULL,
    issuer        TEXT NOT NULL,
    client_id     TEXT NOT NULL,
    -- Empty for a public client that authenticates with PKCE alone. Stored as
    -- written: the provider needs the original bytes, so this cannot be hashed.
    -- Never read back out through the API.
    client_secret TEXT NOT NULL DEFAULT '',
    scopes        TEXT NOT NULL DEFAULT 'openid profile email',
    enabled       BOOLEAN NOT NULL DEFAULT true,
    -- Whether a first sign-in through this provider may create an account, as
    -- opposed to only signing in one that already exists. Independent of the
    -- settings table's signups_enabled, which governs the email-and-password
    -- form only: an operator wanting their own IdP to be the sole way in needs
    -- to be able to close that form and leave this open, and the reverse.
    allow_signup  BOOLEAN NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per (provider, subject) -> account link. The subject is the provider's
-- own immutable id for the person, which is what a returning login is matched
-- on; email is only ever used to find an account the first time, because an
-- email can be reassigned to someone else and a subject cannot.
--
-- A user may hold links from several providers, so the key is the pair, not the
-- user. Deleting a provider drops its links and leaves the accounts alone.
CREATE TABLE oidc_identities (
    provider_id UUID NOT NULL REFERENCES oidc_providers(id) ON DELETE CASCADE,
    subject     TEXT NOT NULL,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email       TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (provider_id, subject)
);

CREATE INDEX idx_oidc_identities_user_id ON oidc_identities (user_id);

-- An account created by a federated login has no password to store, and the
-- column was NOT NULL with no default because a local sign-up always has one.
-- The empty string is not a valid bcrypt hash, so such an account cannot be
-- signed into with a password -- comparing against it always fails.
ALTER TABLE users ALTER COLUMN password_hash SET DEFAULT '';
