-- The rows widened by the up migration are not narrowed again: which subset
-- each one covered is not recorded anywhere, and guessing would either restore
-- too much or break a working integration.
ALTER TABLE integrations ALTER COLUMN all_repos SET DEFAULT false;
