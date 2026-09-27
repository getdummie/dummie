-- One key per provider per user, plus one global key per provider (owner_id
-- NULL) that an admin sets for everyone. The key itself is AES-GCM sealed.
CREATE TABLE llm_keys (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id    UUID REFERENCES users(id) ON DELETE CASCADE,
    provider    TEXT NOT NULL CHECK (provider IN ('zai')),
    plan        TEXT NOT NULL,
    api_key_enc BYTEA NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by  UUID REFERENCES users(id) ON DELETE SET NULL
);

CREATE UNIQUE INDEX llm_keys_user ON llm_keys (owner_id, provider) WHERE owner_id IS NOT NULL;
CREATE UNIQUE INDEX llm_keys_global ON llm_keys (provider) WHERE owner_id IS NULL;
