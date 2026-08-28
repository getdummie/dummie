CREATE TABLE personal_access_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    token_hash TEXT NOT NULL UNIQUE,

    token_prefix TEXT NOT NULL DEFAULT '',

    label TEXT NOT NULL DEFAULT '',

    expires_at TIMESTAMPTZ,

    revoked BOOLEAN NOT NULL DEFAULT false,

    last_used_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_pats_user_id ON personal_access_tokens (user_id);
