-- Zielsystem-Plugins pro Organisation: Aktivierung von Built-ins und
-- hochgeladene Manifest-Plugins (kind=custom, Manifest als JSONB).
-- Built-ins ohne Zeile gelten als aktiviert (Bestandsschutz: eine bereits
-- laufende Zammad-Anbindung bleibt nach dem Update aktiv).
CREATE TABLE target_plugins (
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    kind       TEXT NOT NULL DEFAULT 'builtin' CHECK (kind IN ('builtin', 'custom')),
    enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    manifest   JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, name),
    CHECK (kind <> 'custom' OR manifest IS NOT NULL)
);
