DROP INDEX IF EXISTS agents_hired_idx;
ALTER TABLE agents DROP COLUMN IF EXISTS hired_at;
