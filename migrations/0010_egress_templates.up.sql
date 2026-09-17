-- Egress v2: allowlist per agent instead of platform-global.
-- Setup: reusable templates (host sets) are assigned to agents;
-- plus the agent's own individual hosts. An agent's effective allowlist =
-- Anthropic default (code) + hosts of all assigned templates + own hosts.
-- Plus a decision log (allowed/blocked) and a per-sandbox token,
-- through which the proxy identifies the requesting agent.

-- The global v1 table is dropped (introduced fresh, no user data).
DROP TABLE IF EXISTS egress_allow;

-- Reusable host set, org-scoped.
CREATE TABLE egress_templates (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, name)
);

-- Hosts of a template. Patterns: exact host or "*.suffix".
CREATE TABLE egress_template_hosts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    template_id UUID NOT NULL REFERENCES egress_templates(id) ON DELETE CASCADE,
    pattern     TEXT NOT NULL,
    note        TEXT NOT NULL DEFAULT '',
    UNIQUE (template_id, pattern)
);

-- Assignment template -> agent.
CREATE TABLE agent_egress_templates (
    agent_id    UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    template_id UUID NOT NULL REFERENCES egress_templates(id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, template_id)
);

-- Agent's own individual hosts (on top of templates).
CREATE TABLE agent_egress_hosts (
    id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    pattern  TEXT NOT NULL,
    note     TEXT NOT NULL DEFAULT '',
    UNIQUE (agent_id, pattern)
);

-- Per-sandbox egress token: the proxy identifies the requesting agent
-- via Proxy-Authorization (user=agent_id, password=token). Set anew at
-- sandbox start (rotated); only the hash is stored.
CREATE TABLE agent_egress_tokens (
    agent_id   UUID PRIMARY KEY REFERENCES agents(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Decision log: every egress decision (allowed + blocked).
CREATE TABLE egress_log (
    id         BIGSERIAL PRIMARY KEY,
    agent_id   UUID REFERENCES agents(id) ON DELETE CASCADE,
    host       TEXT NOT NULL,
    method     TEXT NOT NULL DEFAULT '',
    allowed    BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX egress_log_recent ON egress_log (created_at DESC);
CREATE INDEX egress_log_agent ON egress_log (agent_id, created_at DESC);
