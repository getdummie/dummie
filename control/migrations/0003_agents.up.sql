-- 0003_agents: ephemeral enrollment keys and the agent registry.

-- Keys handed to a machine so it can enroll itself once. Stored hashed; the raw
-- key is shown exactly once at creation. Validity is bounded by expires_at
-- and/or max_uses, either of which may be NULL for "unbounded on that axis".
CREATE TABLE agent_enrollment_keys (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key_hash   TEXT NOT NULL UNIQUE,
    key_prefix TEXT NOT NULL DEFAULT '',
    label      TEXT NOT NULL DEFAULT '',
    max_uses   INTEGER,
    uses       INTEGER NOT NULL DEFAULT 0,
    expires_at TIMESTAMPTZ,
    revoked    BOOLEAN NOT NULL DEFAULT false,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per enrolled machine. machine_id is UNIQUE so a re-enroll from the
-- same box (reinstall, lost state file) rotates the token in place instead of
-- creating a duplicate agent.
CREATE TABLE agents (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    machine_id      TEXT NOT NULL UNIQUE,
    hostname        TEXT NOT NULL DEFAULT '',
    token_hash      TEXT NOT NULL UNIQUE,
    status          TEXT NOT NULL DEFAULT 'offline'
                    CHECK (status IN ('online', 'offline')),
    os              TEXT NOT NULL DEFAULT '',
    os_version      TEXT NOT NULL DEFAULT '',
    arch            TEXT NOT NULL DEFAULT '',
    agent_version   TEXT NOT NULL DEFAULT '',
    last_seen_at    TIMESTAMPTZ,
    last_ip         TEXT NOT NULL DEFAULT '',
    enrolled_key_id UUID REFERENCES agent_enrollment_keys(id) ON DELETE SET NULL,
    revoked         BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_agents_status ON agents (status);
