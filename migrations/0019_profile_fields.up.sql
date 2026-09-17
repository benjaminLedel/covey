-- Configurable profile fields: each organisation defines itself which
-- extra fields an employee profile has (e.g. location, department,
-- Slack handle). The definition lives here, the values per person in
-- humans.custom (key → value) — new fields need no schema change.
CREATE TABLE profile_fields (
    id         UUID PRIMARY KEY,
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    key        TEXT NOT NULL, -- slug, derived from the label; key in humans.custom
    label      TEXT NOT NULL, -- display name in UI and team directory
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, key)
);

ALTER TABLE humans ADD COLUMN custom JSONB NOT NULL DEFAULT '{}'::jsonb;
