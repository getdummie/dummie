ALTER TABLE users ALTER COLUMN password_hash DROP DEFAULT;
DROP TABLE IF EXISTS oidc_identities;
DROP TABLE IF EXISTS oidc_providers;
