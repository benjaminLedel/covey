-- One snapshot per agent, and no rules about it (spec/16, "Cleaning up").
--
-- The history was a by-product nobody ordered. What the home store is for is
-- that a lost runner costs time instead of work, and exactly one state does
-- that. Keeping more cost a retention policy, a snapshot list and a restore
-- action -- three pieces of product for a capability that merely fell out of
-- the construction.
--
-- The surplus rows go here and not at the next sync. The constraint below
-- cannot be added while they exist, and an upgrade that left them would decide
-- nothing while the next job quietly decided everything.
DELETE FROM home_snapshots AS h
      USING home_snapshots AS newer
      WHERE h.agent_id = newer.agent_id
        AND (newer.created_at, newer.id) > (h.created_at, h.id);

-- From here the database carries the promise, not a convention in the code: a
-- second row for an agent is a bug, and it fails at the write rather than at
-- the sweep that has to guess which of the two is the home.
ALTER TABLE home_snapshots ADD CONSTRAINT home_snapshots_agent_key UNIQUE (agent_id);

-- Rules about a history that no longer exists. What no manifest references is
-- gone -- there is nothing left to weigh up, so there is nothing to configure.
ALTER TABLE organizations
    DROP COLUMN home_retention_keep,
    DROP COLUMN home_retention_days;
