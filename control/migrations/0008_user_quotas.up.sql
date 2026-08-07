-- 0008_user_quotas: per-user vCPU and memory allowances.
--
-- Recorded, not enforced: nothing in the VM create path reads these yet. They
-- exist so an operator can write down what a user is entitled to before there
-- is anything to check it against.
--
-- NOT NULL with a default rather than nullable: "no row-level limit" and "the
-- default limit" would otherwise be the same value to a reader, and every user
-- is entitled to something. Existing rows get the default by the same clause.
ALTER TABLE users
    ADD COLUMN vcpu_limit       INTEGER NOT NULL DEFAULT 2,
    ADD COLUMN memory_limit_mib INTEGER NOT NULL DEFAULT 1024;
