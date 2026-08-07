-- 0007_settings: operator-editable runtime settings.
--
-- One row per setting, value stored as TEXT and interpreted by the reader. The
-- alternative -- a column per setting -- means a migration every time an
-- operator toggle is added, and these are read a handful of times per request
-- at most, so the loss of type safety at the column level is worth the
-- flexibility. The set of valid keys is enforced in Go, not here: an admin can
-- only write keys the server knows about.
--
-- These replace environment variables for things an operator should be able to
-- change without a redeploy. Anything that must be known before the process can
-- serve traffic (DB URL, JWT secret) stays in the environment.
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL
);

-- Seeded off. This one lets any machine that can reach /enroll join the fleet
-- without a key, so the safe value is the one a fresh install gets.
INSERT INTO settings (key, value) VALUES ('agent_open_enrollment', 'false');
