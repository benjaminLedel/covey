ALTER TABLE home_snapshots DROP COLUMN IF EXISTS duration_ms;
ALTER TABLE organizations
    DROP COLUMN IF EXISTS home_retention_keep,
    DROP COLUMN IF EXISTS home_retention_days;
