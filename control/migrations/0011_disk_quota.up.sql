-- 0011_disk_quota: a per-user disk allowance, and the per-VM figure to sum.
--
-- The limit has no meaning without something to total against it, and until now
-- a VM's disk size only existed inside the spec JSON -- which means summing it
-- would be a JSON scan on every create. So the size gets its own column,
-- written at create time from the same value that goes into the spec.
ALTER TABLE users ADD COLUMN disk_limit_mib INTEGER NOT NULL DEFAULT 10240;

-- 0 for a VM whose disk size was never recorded: one adopted from an client's
-- inventory report, or created before this column existed. That understates a
-- user's usage rather than inventing a number, which is the safer direction to
-- be wrong in for a limit nothing enforces on the host anyway.
ALTER TABLE vms ADD COLUMN disk_mib INTEGER NOT NULL DEFAULT 0;
