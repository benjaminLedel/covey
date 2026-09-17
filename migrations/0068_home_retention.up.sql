-- Retention in the home store, and what a sync cost (spec/16,
-- "Retention" and "Interface").
--
-- A store that grows quietly in the background and whose contents nobody
-- sees is an operational risk — you notice it when the disk is full.
-- That is why both rules belong in the interface, not only in an
-- environment variable.
--
-- The newest snapshot of every agent always stays, even if both rules would
-- catch it: a retention that leaves an agent without a last home is a delete
-- command by detour. That stands in the code (ApplyRetention), not
-- as a CHECK here — it is a rule about the result, not about the values.
ALTER TABLE organizations
    -- How many snapshots to keep per agent. 0 = no limit by count.
    ADD COLUMN home_retention_keep INTEGER NOT NULL DEFAULT 10,
    -- Maximum age in days. 0 = no limit by age.
    ADD COLUMN home_retention_days INTEGER NOT NULL DEFAULT 30;

-- How long the sync ran. The number answers the only question one has
-- about the sleep path: whether it is expensive.
ALTER TABLE home_snapshots ADD COLUMN duration_ms INTEGER NOT NULL DEFAULT 0;
