-- 0024_user_public_key_unique: one key, one account.
--
-- A session is routed to a VM by the key it authenticated with, so two accounts
-- holding the same key are indistinguishable to the proxy.

-- Existing values are canonical "<type> <base64> <comment>", so dropping the
-- third field is enough; the comment is what made a shared key look distinct.
UPDATE users
SET public_key = split_part(public_key, ' ', 1) || ' ' || split_part(public_key, ' ', 2)
WHERE public_key <> '';

-- Partial: public_key is '' for every account with no key on file, and those are
-- not duplicates of each other.
CREATE UNIQUE INDEX users_public_key_key
    ON users (public_key)
    WHERE public_key <> '';
