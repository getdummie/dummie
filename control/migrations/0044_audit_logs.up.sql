CREATE TABLE audit_logs (
    id             BIGSERIAL PRIMARY KEY,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_id       UUID REFERENCES users(id) ON DELETE SET NULL,
    actor_username TEXT NOT NULL DEFAULT '',
    auth_method    TEXT NOT NULL,
    action         TEXT NOT NULL,
    path           TEXT NOT NULL DEFAULT '',
    params         JSONB NOT NULL DEFAULT '{}'::jsonb,
    status         INTEGER NOT NULL,
    ip             TEXT NOT NULL DEFAULT '',
    user_agent     TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_audit_logs_created_at ON audit_logs (created_at DESC);
CREATE INDEX idx_audit_logs_actor ON audit_logs (actor_id, created_at DESC);
