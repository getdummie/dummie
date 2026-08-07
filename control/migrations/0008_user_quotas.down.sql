ALTER TABLE users
    DROP COLUMN IF EXISTS vcpu_limit,
    DROP COLUMN IF EXISTS memory_limit_mib;
