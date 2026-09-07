-- Narrows one attachment to a subset of the repositories its integration
-- covers, so an integration spanning ten repos can give one VM just one.
--
-- No rows for an attachment means it inherits the integration's whole list.
-- That keeps the common case (one integration, one repo, attach and go) a
-- single click, and it is what every existing attachment already means.
--
-- The foreign key is on the pair, so detaching a VM takes its scoping with it
-- rather than leaving rows that would silently apply on a re-attach.
CREATE TABLE vm_integration_repos (
    vm_id          UUID NOT NULL,
    integration_id UUID NOT NULL,
    repo_owner     TEXT NOT NULL,
    repo_name      TEXT NOT NULL,
    PRIMARY KEY (vm_id, integration_id, repo_owner, repo_name),
    FOREIGN KEY (vm_id, integration_id)
        REFERENCES vm_integrations (vm_id, integration_id) ON DELETE CASCADE
);
