ALTER TABLE runners
    DROP COLUMN IF EXISTS description,
    DROP COLUMN IF EXISTS tags,
    DROP COLUMN IF EXISTS version,
    DROP COLUMN IF EXISTS arch,
    DROP COLUMN IF EXISTS protocol;
DROP TABLE IF EXISTS runner_registration_tokens;
