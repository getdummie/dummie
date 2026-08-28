CREATE TABLE oidc_providers (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug          TEXT NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,31}$'),
    display_name  TEXT NOT NULL,
    issuer        TEXT NOT NULL,
    client_id     TEXT NOT NULL,
    client_secret TEXT NOT NULL DEFAULT '',
    scopes        TEXT NOT NULL DEFAULT 'openid profile email',
    enabled       BOOLEAN NOT NULL DEFAULT true,
    allow_signup  BOOLEAN NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

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

ALTER TABLE users ALTER COLUMN password_hash SET DEFAULT '';
