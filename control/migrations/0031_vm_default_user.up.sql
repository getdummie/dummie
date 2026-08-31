-- The account an ssh or console session lands in. Empty means "whatever the os
-- image declared", which is the usual case, so this is only set when someone
-- wants a different one for this vm.
--
-- It is deliberately not the user the workload runs as: that is baked into the
-- rootfs when the vm is created and cannot be moved afterwards, so this only
-- decides where a session lands.
ALTER TABLE vms
    ADD COLUMN default_user TEXT NOT NULL DEFAULT ''
        CHECK (default_user = '' OR default_user ~ '^[a-zA-Z0-9._][a-zA-Z0-9._-]*$');
