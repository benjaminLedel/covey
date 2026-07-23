-- MCP-Server als dritter Zielsystem-Plugin-Typ (kind='mcp'). Die Config (URL,
-- Auth, entdeckte Tool-Liste) liegt wie beim Manifest in der Spalte manifest
-- (JSONB) — der Broker holt das Token zur Laufzeit aus dem SecretStore, nie
-- aus dieser Zeile.
--
-- Die CHECK-Constraints aus 0008 sind inline/auto-benannt. Statt Namen zu
-- raten, entfernen wir alle CHECKs der Tabelle und legen sie benannt neu an.
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

-- custom und mcp brauchen ihre Definition in manifest; builtin darf leer bleiben.
ALTER TABLE target_plugins ADD CONSTRAINT target_plugins_manifest_check
    CHECK (kind = 'builtin' OR manifest IS NOT NULL);

-- Per-Agent-Tool-Zuweisung: welche Tools eines Zielsystems ein Agent nutzen
-- darf. KEINE Zeile für (agent, system) = alle Tools erlaubt (rückwärts-
-- kompatibel, fail-open pro System). Existiert mindestens eine Zeile, gilt die
-- Zuweisung als Allowlist — nur gelistete Tools sind erlaubt (fail-closed).
CREATE TABLE agent_target_tools (
    agent_id   UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    system     TEXT NOT NULL,
    tool       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, system, tool)
);
CREATE INDEX agent_target_tools_by_system ON agent_target_tools (agent_id, system);
