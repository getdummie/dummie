-- 0005_vm_inventory: statuses for VMs the control plane did not create.
--
-- Agents now report the VMs they are actually running, so a row no longer only
-- ever describes a create this server asked for. Two states follow from that:
-- 'stopped' for a VM that exists on its host but is not running, and 'gone' for
-- one that has disappeared from the host's inventory entirely.
--
-- 'gone' rather than deleting the row: a VM that vanished is exactly the thing
-- an operator wants to see, and silently dropping it makes the table agree with
-- the fleet by forgetting the disagreement.
ALTER TABLE vms DROP CONSTRAINT vms_status_check;
ALTER TABLE vms ADD CONSTRAINT vms_status_check
    CHECK (status IN ('pending', 'running', 'stopped', 'failed', 'gone'));
