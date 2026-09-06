ALTER TABLE clients DROP COLUMN IF EXISTS intproxy_version;

DROP TABLE IF EXISTS vm_integrations;
DROP TABLE IF EXISTS integration_repos;
DROP TABLE IF EXISTS integrations;
DROP TABLE IF EXISTS github_installations;
DROP TABLE IF EXISTS github_apps;
