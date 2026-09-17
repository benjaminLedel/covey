-- Target system plugins per organisation: activation of built-ins and
-- uploaded manifest plugins (kind=custom, manifest as JSONB).
-- Built-ins without a row count as enabled (grandfathering: a Zammad
-- connection already running stays active after the update).
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
