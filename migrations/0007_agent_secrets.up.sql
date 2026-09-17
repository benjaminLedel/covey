-- Secrets with agent scope (spec/04): agent_id NULL = org-wide secret (as
-- before), set = agent's own secret, resolvable only for that agent.
ALTER TABLE secrets DROP CONSTRAINT secrets_pkey;
ALTER TABLE secrets ADD COLUMN agent_id UUID REFERENCES agents(id) ON DELETE CASCADE;
CREATE UNIQUE INDEX uq_secrets_org   ON secrets (org_id, key) WHERE agent_id IS NULL;
CREATE UNIQUE INDEX uq_secrets_agent ON secrets (org_id, agent_id, key) WHERE agent_id IS NOT NULL;

-- Explicit assignment of org-wide secrets to agents: without an assignment an
-- org secret applies to all agents (default), with assignments only to those.
CREATE TABLE secret_assignments (
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    agent_id   UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, key, agent_id)
);
