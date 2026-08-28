
UPDATE users
SET public_key = split_part(public_key, ' ', 1) || ' ' || split_part(public_key, ' ', 2)
WHERE public_key <> '';

CREATE UNIQUE INDEX users_public_key_key
    ON users (public_key)
    WHERE public_key <> '';
