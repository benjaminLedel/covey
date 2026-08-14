ALTER TABLE target_plugins DROP CONSTRAINT IF EXISTS target_plugins_provenance_check;
ALTER TABLE target_plugins
    DROP COLUMN IF EXISTS source,
    DROP COLUMN IF EXISTS source_version,
    DROP COLUMN IF EXISTS source_digest;
