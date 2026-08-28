
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

CREATE UNIQUE INDEX idx_vms_client_vm_id ON vms (client_id, vm_id) WHERE vm_id <> '';
CREATE INDEX idx_vms_client_id ON vms (client_id);
CREATE INDEX idx_vms_status ON vms (status);
