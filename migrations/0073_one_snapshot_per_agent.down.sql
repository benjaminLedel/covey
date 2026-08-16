-- The settings come back with their old defaults, and the table accepts more
-- than one row per agent again.
--
-- What does not come back are the snapshots the up migration deleted. A down
-- migration can restore the shape of the schema; it cannot restore blocks that
-- the next cleanup has already swept, and pretending otherwise here would be
-- the more dangerous half-truth.
ALTER TABLE home_snapshots DROP CONSTRAINT IF EXISTS home_snapshots_agent_key;

ALTER TABLE organizations
    ADD COLUMN home_retention_keep INTEGER NOT NULL DEFAULT 10,
    ADD COLUMN home_retention_days INTEGER NOT NULL DEFAULT 30;
