-- Egress v2: Allowlist pro Agent statt plattform-global.
-- Aufbau: wiederverwendbare Templates (Host-Sets) werden Agenten zugewiesen;
-- dazu agent-eigene Einzel-Hosts. Effektive Allowlist eines Agenten =
-- Anthropic-Default (Code) + Hosts aller zugewiesenen Templates + eigene Hosts.
-- Dazu ein Entscheidungs-Log (erlaubt/blockiert) und ein per-Sandbox-Token,
-- über das der Proxy den anfragenden Agenten identifiziert.

-- Die globale v1-Tabelle entfällt (frisch eingeführt, keine Nutzdaten).
DROP TABLE IF EXISTS egress_allow;

-- Wiederverwendbares Host-Set, org-scoped.
CREATE TABLE egress_templates (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, name)
);

-- Hosts eines Templates. Muster: exakter Host oder "*.suffix".
CREATE TABLE egress_template_hosts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    template_id UUID NOT NULL REFERENCES egress_templates(id) ON DELETE CASCADE,
    pattern     TEXT NOT NULL,
    note        TEXT NOT NULL DEFAULT '',
    UNIQUE (template_id, pattern)
);

-- Zuweisung Template -> Agent.
CREATE TABLE agent_egress_templates (
    agent_id    UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    template_id UUID NOT NULL REFERENCES egress_templates(id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, template_id)
);

-- Agent-eigene Einzel-Hosts (zusätzlich zu Templates).
CREATE TABLE agent_egress_hosts (
    id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    pattern  TEXT NOT NULL,
    note     TEXT NOT NULL DEFAULT '',
    UNIQUE (agent_id, pattern)
);

-- Per-Sandbox-Egress-Token: Der Proxy identifiziert den anfragenden Agenten
-- über die Proxy-Authorization (Nutzer=agent_id, Passwort=Token). Wird beim
-- Sandbox-Start neu gesetzt (rotiert); nur der Hash wird gespeichert.
CREATE TABLE agent_egress_tokens (
    agent_id   UUID PRIMARY KEY REFERENCES agents(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Entscheidungs-Log: jede Egress-Entscheidung (erlaubt + blockiert).
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
