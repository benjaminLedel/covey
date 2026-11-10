-- One Teams app per installation (#710): which Microsoft tenant belongs to
-- which organisation, and what the shared bot knows of each tenant.
--
-- Everything hangs off the tenant link and goes with it: unlinking a tenant
-- forgets its teams, conversations and channel defaults in one statement.

CREATE TABLE teamsapp_tenants (
    tenant_id   TEXT PRIMARY KEY,
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    -- The Microsoft account that proved the link, as the ID token named it.
    linked_by   TEXT NOT NULL DEFAULT '',
    linked_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX teamsapp_tenants_org ON teamsapp_tenants (org_id);

-- The teams the bot is installed in. service_url comes from a verified
-- delivery (the token's serviceurl claim), never from an agent.
CREATE TABLE teamsapp_teams (
    tenant_id   TEXT NOT NULL REFERENCES teamsapp_tenants(tenant_id) ON DELETE CASCADE,
    team_id     TEXT NOT NULL,
    name        TEXT NOT NULL DEFAULT '',
    service_url TEXT NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, team_id)
);

-- A conversation the bot has seen or opened: a personal or group chat, or a
-- thread in a channel. agent_id is the colleague it is bound to.
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

-- The colleague a channel goes to when a message names nobody.
CREATE TABLE teamsapp_channel_agents (
    tenant_id   TEXT NOT NULL REFERENCES teamsapp_tenants(tenant_id) ON DELETE CASCADE,
    channel_id  TEXT NOT NULL,
    agent_id    UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    PRIMARY KEY (tenant_id, channel_id)
);
