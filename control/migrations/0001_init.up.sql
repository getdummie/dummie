-- 0001_init: initial schema placeholder.
CREATE TABLE IF NOT EXISTS control_meta (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
