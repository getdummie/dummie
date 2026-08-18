-- 0021_personal_access_tokens: long-lived credentials a user mints for scripts.
--
-- Distinct from refresh_tokens, which are sessions a browser holds: those are
-- issued by signing in, expire in days, and exist to keep a tab logged in. A
-- personal access token is issued deliberately, may never expire, and is the
-- credential a CI job or a curl carries. Conflating the two would mean signing
-- out of a laptop killed a pipeline.
--
-- Stored hashed. The raw token is shown exactly once at creation, the same
-- contract client_enrollment_keys already makes.
CREATE TABLE personal_access_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- CASCADE, unlike the enrollment keys' SET NULL: a token is not an audit
    -- record of an admin's decision, it is a way to act as this account. With
    -- the account gone there is nothing left for it to act as.
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    token_hash TEXT NOT NULL UNIQUE,

    -- The leading characters of the raw token, kept so the list can show which
    -- row is which. Enough to recognise a token you still have, useless to
    -- anyone who only has this.
    token_prefix TEXT NOT NULL DEFAULT '',

    label TEXT NOT NULL DEFAULT '',

    -- NULL = never expires. A token minted for a long-running deployment has no
    -- honest expiry date, and forcing one would only teach people to pick a
    -- decade.
    expires_at TIMESTAMPTZ,

    revoked BOOLEAN NOT NULL DEFAULT false,

    -- Stamped on every authenticated request. This is what makes "which of
    -- these can I safely delete" answerable.
    last_used_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_pats_user_id ON personal_access_tokens (user_id);
