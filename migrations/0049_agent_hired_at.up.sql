-- The draft: an agent that exists, that nobody has hired yet.
--
-- Technically the kill switch would have been enough — a dead agent does not
-- run either. But it would put two different facts in the same field: "this
-- one was stopped" and "this one has not started yet". And hiring is not a
-- flag, but a point in time: it will later stand in the employee profile
-- next to that of a human. Details in spec/20-hiring-and-setup.md.
--
-- NULL = draft: not dispatched, no heartbeat, no live webhook, no
-- sandbox, no costs. Tasks may still lie in the backlog and wait for the
-- first day.
ALTER TABLE agents ADD COLUMN hired_at TIMESTAMPTZ;

-- Nobody wakes up as a draft: everything that exists today has been working
-- since it was created.
UPDATE agents SET hired_at = created_at;

-- No index on this column. Every query that filters on it — the tick, the
-- heartbeats, the team directory — looks for `hired_at IS NOT NULL`, and that
-- matches practically every row: an index on it is not used. The reverse
-- partial index (IS NULL) would find the drafts quickly, but nobody asks for
-- that in SQL — the interface sorts the list in the browser.
