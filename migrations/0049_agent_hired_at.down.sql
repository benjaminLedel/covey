-- DROP INDEX is still here: development databases that ran an earlier
-- version of this migration still carry agents_hired_idx.
DROP INDEX IF EXISTS agents_hired_idx;
ALTER TABLE agents DROP COLUMN IF EXISTS hired_at;
