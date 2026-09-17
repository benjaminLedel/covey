-- MCP servers as a third target system plugin type (kind='mcp'). The config (URL,
-- auth, discovered tool list) lies in the manifest column like the manifest
-- (JSONB) — the broker fetches the token at runtime from the SecretStore, never
-- from this row.
--
-- The CHECK constraints from 0008 are inline/auto-named. Instead of guessing
-- names, we drop all CHECKs of the table and recreate them under names.
DO $$
DECLARE c text;
BEGIN
    FOR c IN SELECT conname FROM pg_constraint
        WHERE conrelid = 'target_plugins'::regclass AND contype = 'c'
    LOOP
        EXECUTE format('ALTER TABLE target_plugins DROP CONSTRAINT %I', c);
    END LOOP;
END $$;

ALTER TABLE target_plugins ADD CONSTRAINT target_plugins_kind_check
    CHECK (kind IN ('builtin', 'custom', 'mcp'));

-- custom and mcp need their definition in manifest; builtin may stay empty.
ALTER TABLE target_plugins ADD CONSTRAINT target_plugins_manifest_check
    CHECK (kind = 'builtin' OR manifest IS NOT NULL);

-- Per-agent tool assignment: which tools of a target system an agent may
-- use. NO row for (agent, system) = all tools allowed (backward-
-- compatible, fail-open per system). If at least one row exists, the
-- assignment is an allowlist — only listed tools are allowed (fail-closed).
CREATE TABLE agent_target_tools (
    agent_id   UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    system     TEXT NOT NULL,
    tool       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, system, tool)
);
CREATE INDEX agent_target_tools_by_system ON agent_target_tools (agent_id, system);
