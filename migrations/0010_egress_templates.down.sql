DROP TABLE IF EXISTS egress_log;
DROP TABLE IF EXISTS agent_egress_tokens;
DROP TABLE IF EXISTS agent_egress_hosts;
DROP TABLE IF EXISTS agent_egress_templates;
DROP TABLE IF EXISTS egress_template_hosts;
DROP TABLE IF EXISTS egress_templates;

-- v1-Tabelle wiederherstellen (leer).
CREATE TABLE egress_allow (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pattern    TEXT NOT NULL UNIQUE,
    note       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
