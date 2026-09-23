-- The kernel a vm boots, by catalogue id, so the console can show it and swap it.
-- Existing rows are matched on the key's uuid segment inside the presigned url.
ALTER TABLE vms ADD COLUMN kernel_id UUID REFERENCES kernels (id) ON DELETE SET NULL;

UPDATE vms v
SET kernel_id = k.id
FROM kernels k
WHERE v.boot = 'direct'
  AND position(regexp_replace(k.object_key, '[^/]*$', '') IN coalesce(v.spec ->> 'kernel', '')) > 0;
