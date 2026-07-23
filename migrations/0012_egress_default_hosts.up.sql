-- Basis-Allowlist pro Organisation: Hosts, die JEDER Agent der Org erreichen
-- darf — bisher als Code-Default fest verdrahtet (api.anthropic.com), jetzt
-- konfigurierbar über die UI. Bestehende Orgs werden mit dem bisherigen
-- Default geseedet, damit sich das Verhalten durch die Migration nicht ändert;
-- neue Orgs seedet die Org-Anlage.
CREATE TABLE egress_default_hosts (
    id      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id  UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    pattern TEXT NOT NULL,
    note    TEXT NOT NULL DEFAULT '',
    UNIQUE (org_id, pattern)
);

INSERT INTO egress_default_hosts (org_id, pattern, note)
SELECT id, 'api.anthropic.com', 'LLM-Endpunkt der Claude-Runtime'
FROM organizations;
