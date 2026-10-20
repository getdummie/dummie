-- Written into a vm's global git config when it is created. Empty leaves git
-- alone, and an edit only reaches vms created after it.
ALTER TABLE users
    ADD COLUMN git_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN git_email TEXT NOT NULL DEFAULT '';
