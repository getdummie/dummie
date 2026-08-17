-- 0004_vms: the VM registry, and host metrics on the clients that run them.

-- Host metrics reported by the client over its socket. They live on the client
-- row rather than in a time series because the control plane only ever asks
-- "what does this host look like now" -- for scheduling and for the fleet view.
-- Keeping history is a separate table when someone actually needs a graph.
--
-- NOT NULL DEFAULT 0 so every consumer can read a number without checking a
-- flag; metrics_at is the one nullable column, and it is what distinguishes
-- "reported zero" from "never reported".
ALTER TABLE clients
    ADD COLUMN cpu_count        INTEGER          NOT NULL DEFAULT 0,
    ADD COLUMN cpu_percent      DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN load1            DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN load5            DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN load15           DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN mem_total_bytes  BIGINT           NOT NULL DEFAULT 0,
    ADD COLUMN mem_used_bytes   BIGINT           NOT NULL DEFAULT 0,
    ADD COLUMN disk_total_bytes BIGINT           NOT NULL DEFAULT 0,
    ADD COLUMN disk_used_bytes  BIGINT           NOT NULL DEFAULT 0,
    ADD COLUMN uptime_seconds   BIGINT           NOT NULL DEFAULT 0,
    ADD COLUMN metrics_at       TIMESTAMPTZ;

-- One row per VM the control plane asked an client to run. The row is written
-- before the job is pushed, so a create that is never answered leaves evidence
-- rather than nothing at all -- 'pending' is a real state, not a gap.
--
-- vm_id is the client's own short id and is empty until the client reports back:
-- the id is allocated on the host, not here. spec is the request as sent, so
-- the row still describes what was asked for even if the create failed.
CREATE TABLE vms (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id  UUID NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    vm_id      TEXT NOT NULL DEFAULT '',
    name       TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL DEFAULT 'pending'
               CHECK (status IN ('pending', 'running', 'failed')),
    boot       TEXT NOT NULL DEFAULT '',
    cpus       INTEGER NOT NULL DEFAULT 0,
    memory_mib INTEGER NOT NULL DEFAULT 0,
    ip         TEXT NOT NULL DEFAULT '',
    spec       JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ
);

-- A VM id is only unique within its host, and only once the host has assigned
-- one -- hence the partial index rather than a plain unique constraint.
CREATE UNIQUE INDEX idx_vms_client_vm_id ON vms (client_id, vm_id) WHERE vm_id <> '';
CREATE INDEX idx_vms_client_id ON vms (client_id);
CREATE INDEX idx_vms_status ON vms (status);
