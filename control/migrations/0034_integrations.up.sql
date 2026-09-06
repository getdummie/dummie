-- The GitHub App itself. One per control plane. The private key never leaves
-- this table: hosts ask for minted installation tokens instead, which is the
-- whole point -- a compromised host cannot mint anything for a vm it does not
-- own, and no credential ever reaches a guest.
CREATE TABLE github_apps (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id      BIGINT NOT NULL UNIQUE,
    slug        TEXT NOT NULL DEFAULT '',
    name        TEXT NOT NULL DEFAULT '',
    private_key TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per account the app was installed into.
CREATE TABLE github_installations (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_pk               UUID   NOT NULL REFERENCES github_apps(id) ON DELETE CASCADE,
    installation_id      BIGINT NOT NULL,
    account_login        TEXT   NOT NULL,
    account_type         TEXT   NOT NULL DEFAULT 'Organization',
    repository_selection TEXT   NOT NULL DEFAULT 'selected'
                         CHECK (repository_selection IN ('selected', 'all')),
    installed_by         UUID REFERENCES users(id) ON DELETE SET NULL,
    -- Set when a mint gets a 404 or 401 from github: the installation was
    -- removed or suspended there, and the console has to say "reconnect"
    -- rather than fail every clone with a 502.
    suspended            BOOLEAN NOT NULL DEFAULT false,
    last_error           TEXT NOT NULL DEFAULT '',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (app_pk, installation_id)
);

-- A named record a user owns. Inert until a row in vm_integrations attaches it.
CREATE TABLE integrations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    kind            TEXT NOT NULL DEFAULT 'github' CHECK (kind IN ('github')),
    installation_pk UUID REFERENCES github_installations(id) ON DELETE SET NULL,
    -- true means every repo the installation covers, which is only offerable
    -- when the installation itself is repository_selection = 'all'.
    all_repos       BOOLEAN NOT NULL DEFAULT false,
    readonly        BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner_id, name)
);

ALTER TABLE integrations ADD CONSTRAINT integrations_name_shape CHECK (
    char_length(name) BETWEEN 3 AND 52
    AND name ~ '^[a-z0-9]+(-[a-z0-9]+)*$'
);

CREATE TABLE integration_repos (
    integration_id UUID NOT NULL REFERENCES integrations(id) ON DELETE CASCADE,
    repo_owner     TEXT NOT NULL,
    repo_name      TEXT NOT NULL,
    PRIMARY KEY (integration_id, repo_owner, repo_name)
);

-- The attachment. Nothing is reachable until a row exists here. Several
-- integrations may attach to one vm: the hostname is the aggregate
-- github.int.<tld> and authorization is per (vm, repo), so there is no
-- ambiguity about which token to mint.
CREATE TABLE vm_integrations (
    vm_id          UUID NOT NULL REFERENCES vms(id) ON DELETE CASCADE,
    integration_id UUID NOT NULL REFERENCES integrations(id) ON DELETE CASCADE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (vm_id, integration_id)
);

CREATE INDEX idx_integrations_owner ON integrations (owner_id);
CREATE INDEX idx_vm_integrations_integration ON vm_integrations (integration_id);

ALTER TABLE clients ADD COLUMN intproxy_version TEXT NOT NULL DEFAULT '';
