-- Back to the old set. An agent that is caught in `securing` at this moment
-- would violate the constraint, so it is put to sleep first: that is where it
-- would have ended up a moment later anyway.
UPDATE agents SET status = 'sleeping' WHERE status = 'securing';
ALTER TABLE agents DROP CONSTRAINT IF EXISTS agents_status_check;
ALTER TABLE agents ADD CONSTRAINT agents_status_check
    CHECK (status IN ('sleeping','triggered','triage','working','killed'));
