-- The settings and the index go; what the retention already deleted does not
-- come back. A schema can be rolled back, a transcript cannot.
DROP INDEX IF EXISTS idx_recording_runtime_alter;

ALTER TABLE agents DROP COLUMN IF EXISTS recording_retention_days;
ALTER TABLE organizations DROP COLUMN IF EXISTS recording_retention_days;
