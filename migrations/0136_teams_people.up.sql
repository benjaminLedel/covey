-- The agent works as the person who wrote (#713).
--
-- teamsuser_people: a Microsoft user's delegated token, obtained through
-- single sign-on in the chat with the installation's Teams app; sealed with
-- the master key (associated data: tenant and object id). It belongs to the
-- organisation that linked the tenant and goes with that link.
CREATE TABLE teamsuser_people (
    tenant_id   TEXT NOT NULL REFERENCES teamsapp_tenants(tenant_id) ON DELETE CASCADE,
    oid         TEXT NOT NULL,
    ms_user     TEXT NOT NULL DEFAULT '',
    nonce       BYTEA NOT NULL,
    ciphertext  BYTEA NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, oid)
);

-- Whose message a task is working on. Moves to whoever wrote last into the
-- conversation the task waits on.
CREATE TABLE teams_task_people (
    task_id     UUID PRIMARY KEY REFERENCES backlog_tasks(id) ON DELETE CASCADE,
    tenant_id   TEXT NOT NULL,
    oid         TEXT NOT NULL
);

-- A message kept while its writer signs in; handed to the agent afterwards.
CREATE TABLE teamsapp_pending (
    tenant_id   TEXT NOT NULL REFERENCES teamsapp_tenants(tenant_id) ON DELETE CASCADE,
    oid         TEXT NOT NULL,
    activity    JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, oid)
);
