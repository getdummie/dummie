-- Pending github app installs, keyed by the hash of a random state.
--
-- This is deliberately server-side rather than a cookie: the row says which
-- integration an installation gets bound to, which is an authorization
-- decision, and the oidc flow cookie it would otherwise mirror is unsigned.
-- Keeping it here means the callback cannot be pointed at someone else's
-- integration.
CREATE TABLE github_install_states (
    state_hash     TEXT PRIMARY KEY,
    owner_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    integration_id UUID NOT NULL REFERENCES integrations(id) ON DELETE CASCADE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_github_install_states_created ON github_install_states (created_at);
