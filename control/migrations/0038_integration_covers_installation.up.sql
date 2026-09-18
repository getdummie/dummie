-- An integration now covers whatever github granted its installation, with no
-- second list to keep in sync. The console stopped asking which of the granted
-- repositories to cover, so all_repos is the only coherent state for a row.
--
-- all_repos already meant "follow the installation". It was only offerable when
-- the installation itself was repository_selection = 'all', but that gate was
-- never a technical one: minting omits the repositories field when the list is
-- empty, and github then scopes the token to exactly what the installation
-- holds -- the selected repositories, for a 'selected' install.
--
-- WIDENING. The update below covers every integration that had been narrowed to
-- a subset of its grant: those rows now reach every repository github granted
-- the installation. Narrowing moves to github (change the installation) and to
-- the per-VM scope in vm_integration_repos, which this does not touch.
ALTER TABLE integrations ALTER COLUMN all_repos SET DEFAULT true;

UPDATE integrations SET all_repos = true, updated_at = now() WHERE NOT all_repos;
