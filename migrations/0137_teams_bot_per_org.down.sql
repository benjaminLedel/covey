DROP TABLE IF EXISTS teamsapp_pending;
DROP TABLE IF EXISTS teams_task_people;
DROP TABLE IF EXISTS teamsuser_people;
DROP TABLE IF EXISTS teamsapp_channel_agents;
DROP TABLE IF EXISTS teamsapp_conversations;
DROP TABLE IF EXISTS teamsapp_teams;
DROP TABLE IF EXISTS teamsapp_bots;

CREATE TABLE teamsapp_tenants (
    tenant_id   TEXT PRIMARY KEY,
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    linked_by   TEXT NOT NULL DEFAULT '',
    linked_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX teamsapp_tenants_org ON teamsapp_tenants (org_id);
CREATE TABLE teamsapp_teams (
    tenant_id   TEXT NOT NULL REFERENCES teamsapp_tenants(tenant_id) ON DELETE CASCADE,
    team_id     TEXT NOT NULL,
    name        TEXT NOT NULL DEFAULT '',
    service_url TEXT NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, team_id)
);
CREATE TABLE teamsapp_conversations (
    tenant_id       TEXT NOT NULL REFERENCES teamsapp_tenants(tenant_id) ON DELETE CASCADE,
    conversation_id TEXT NOT NULL,
    kind            TEXT NOT NULL,
    team_id         TEXT NOT NULL DEFAULT '',
    channel_id      TEXT NOT NULL DEFAULT '',
    agent_id        UUID REFERENCES agents(id) ON DELETE SET NULL,
    service_url     TEXT NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, conversation_id)
);
CREATE TABLE teamsapp_channel_agents (
    tenant_id   TEXT NOT NULL REFERENCES teamsapp_tenants(tenant_id) ON DELETE CASCADE,
    channel_id  TEXT NOT NULL,
    agent_id    UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    PRIMARY KEY (tenant_id, channel_id)
);
CREATE TABLE teamsuser_people (
    tenant_id   TEXT NOT NULL REFERENCES teamsapp_tenants(tenant_id) ON DELETE CASCADE,
    oid         TEXT NOT NULL,
    ms_user     TEXT NOT NULL DEFAULT '',
    nonce       BYTEA NOT NULL,
    ciphertext  BYTEA NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, oid)
);
CREATE TABLE teams_task_people (
    task_id     UUID PRIMARY KEY REFERENCES backlog_tasks(id) ON DELETE CASCADE,
    tenant_id   TEXT NOT NULL,
    oid         TEXT NOT NULL
);
CREATE TABLE teamsapp_pending (
    tenant_id   TEXT NOT NULL REFERENCES teamsapp_tenants(tenant_id) ON DELETE CASCADE,
    oid         TEXT NOT NULL,
    activity    JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, oid)
);
