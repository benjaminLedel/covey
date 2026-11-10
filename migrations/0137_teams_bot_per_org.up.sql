-- Teams bots per organisation, configured in the interface (#715).
--
-- The tables of 0134–0136 were keyed by Microsoft tenant, for one bot per
-- installation configured by environment. They held nothing on any
-- installation when this was written; they are rebuilt keyed by the bot.

DROP TABLE IF EXISTS teamsapp_pending;
DROP TABLE IF EXISTS teams_task_people;
DROP TABLE IF EXISTS teamsuser_people;
DROP TABLE IF EXISTS teamsapp_channel_agents;
DROP TABLE IF EXISTS teamsapp_conversations;
DROP TABLE IF EXISTS teamsapp_teams;
DROP TABLE IF EXISTS teamsapp_tenants;
DELETE FROM teamsuser_delegations;

-- An organisation's bot. The client secret is sealed with the master key
-- (associated data: the bot's id). Delegated access ("Teams as a person")
-- may use an app registration of its own; empty: the bot's.
CREATE TABLE teamsapp_bots (
    id                UUID PRIMARY KEY,
    org_id            UUID NOT NULL UNIQUE REFERENCES organizations(id) ON DELETE CASCADE,
    name              TEXT NOT NULL,
    app_id            TEXT NOT NULL UNIQUE,
    tenant_id         TEXT NOT NULL,
    secret_nonce      BYTEA NOT NULL,
    secret_ct         BYTEA NOT NULL,
    sso_connection    TEXT NOT NULL DEFAULT '',
    deleg_app_id      TEXT NOT NULL DEFAULT '',
    deleg_nonce       BYTEA,
    deleg_ct          BYTEA,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE teamsapp_teams (
    bot_id      UUID NOT NULL REFERENCES teamsapp_bots(id) ON DELETE CASCADE,
    team_id     TEXT NOT NULL,
    name        TEXT NOT NULL DEFAULT '',
    service_url TEXT NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (bot_id, team_id)
);

CREATE TABLE teamsapp_conversations (
    bot_id          UUID NOT NULL REFERENCES teamsapp_bots(id) ON DELETE CASCADE,
    conversation_id TEXT NOT NULL,
    kind            TEXT NOT NULL,
    team_id         TEXT NOT NULL DEFAULT '',
    channel_id      TEXT NOT NULL DEFAULT '',
    agent_id        UUID REFERENCES agents(id) ON DELETE SET NULL,
    service_url     TEXT NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (bot_id, conversation_id)
);

CREATE TABLE teamsapp_channel_agents (
    bot_id      UUID NOT NULL REFERENCES teamsapp_bots(id) ON DELETE CASCADE,
    channel_id  TEXT NOT NULL,
    agent_id    UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    PRIMARY KEY (bot_id, channel_id)
);

-- A Microsoft user's delegated token (sealed; associated data: bot and oid).
CREATE TABLE teamsuser_people (
    bot_id      UUID NOT NULL REFERENCES teamsapp_bots(id) ON DELETE CASCADE,
    oid         TEXT NOT NULL,
    ms_user     TEXT NOT NULL DEFAULT '',
    nonce       BYTEA NOT NULL,
    ciphertext  BYTEA NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (bot_id, oid)
);

CREATE TABLE teams_task_people (
    task_id     UUID PRIMARY KEY REFERENCES backlog_tasks(id) ON DELETE CASCADE,
    bot_id      UUID NOT NULL REFERENCES teamsapp_bots(id) ON DELETE CASCADE,
    oid         TEXT NOT NULL
);

CREATE TABLE teamsapp_pending (
    bot_id      UUID NOT NULL REFERENCES teamsapp_bots(id) ON DELETE CASCADE,
    oid         TEXT NOT NULL,
    activity    JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (bot_id, oid)
);
