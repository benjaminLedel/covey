-- Konfigurierbare Profilfelder: jede Organisation definiert selbst, welche
-- zusätzlichen Felder ein Mitarbeiter-Profil hat (z. B. Standort, Abteilung,
-- Slack-Handle). Die Definition liegt hier, die Werte pro Person in
-- humans.custom (key → wert) — neue Felder brauchen keinen Schema-Change.
CREATE TABLE profile_fields (
    id         UUID PRIMARY KEY,
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    key        TEXT NOT NULL, -- slug, aus dem Label abgeleitet; Schlüssel in humans.custom
    label      TEXT NOT NULL, -- Anzeigename in UI und Team-Verzeichnis
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, key)
);

ALTER TABLE humans ADD COLUMN custom JSONB NOT NULL DEFAULT '{}'::jsonb;
