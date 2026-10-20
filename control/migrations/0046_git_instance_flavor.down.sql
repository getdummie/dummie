DELETE FROM gitlab_instances WHERE flavor <> 'gitlab';
ALTER TABLE gitlab_instances DROP COLUMN IF EXISTS flavor;
