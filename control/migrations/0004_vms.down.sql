DROP TABLE IF EXISTS vms;

ALTER TABLE clients
    DROP COLUMN IF EXISTS cpu_count,
    DROP COLUMN IF EXISTS cpu_percent,
    DROP COLUMN IF EXISTS load1,
    DROP COLUMN IF EXISTS load5,
    DROP COLUMN IF EXISTS load15,
    DROP COLUMN IF EXISTS mem_total_bytes,
    DROP COLUMN IF EXISTS mem_used_bytes,
    DROP COLUMN IF EXISTS disk_total_bytes,
    DROP COLUMN IF EXISTS disk_used_bytes,
    DROP COLUMN IF EXISTS uptime_seconds,
    DROP COLUMN IF EXISTS metrics_at;
