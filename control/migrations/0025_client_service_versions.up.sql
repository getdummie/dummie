-- Which build of dclient, dpipe and dproxy each host runs. Previously each host
-- decided for itself in /etc/dclient/config.yaml, which meant upgrading the
-- fleet was an ssh loop and nothing in the control plane knew what was actually
-- installed anywhere.
--
-- A version is a bare release number the client turns into a github release URL,
-- the same way the fleet-wide vector_version setting already works. The
-- accompanying download_url overrides it when set, which is how a custom build
-- gets onto one host without cutting a release for it.
--
-- Empty rather than nullable, like the rest of this table: an empty version means
-- "whatever this control server's own version is", so a fleet that never touches
-- these columns tracks the server it is talking to.
ALTER TABLE clients
    ADD COLUMN dclient_version      TEXT NOT NULL DEFAULT '',
    ADD COLUMN dclient_download_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN dpipe_version        TEXT NOT NULL DEFAULT '',
    ADD COLUMN dpipe_download_url   TEXT NOT NULL DEFAULT '',
    ADD COLUMN proxy_version        TEXT NOT NULL DEFAULT '',
    ADD COLUMN proxy_download_url   TEXT NOT NULL DEFAULT '';

-- What dpipe and dproxy actually are, as opposed to what the columns above say
-- they should be. Read off the binaries themselves and reported in the hello, and
-- again after every services job -- an upgrade replaces dpipe in place without
-- reconnecting, so waiting for the next hello would leave these stale for as long
-- as the host stayed up.
--
-- dclient needs no column here: it reports its own build as client_version.
--
-- Empty means the host has not said, which covers both "not installed" and
-- "installed but could not be asked". The screen shows a dash either way; the
-- distinction is not one an operator can act on differently.
ALTER TABLE clients
    ADD COLUMN dpipe_installed_version TEXT NOT NULL DEFAULT '',
    ADD COLUMN proxy_installed_version TEXT NOT NULL DEFAULT '';
