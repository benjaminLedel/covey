-- How long the verbatim run is kept (spec/06, "How long the verbatim record is
-- kept").
--
-- The raw course of a run is what makes the recording big: measured on one
-- installation, 206,000 of 262,000 events and 376 of 384 MB were kind='runtime'
-- alone. What sits around it -- which action was performed, which credential
-- was asked for, what somebody approved -- is a fraction of that, and it is the
-- audit trail plus the basis of every indicator (spec/17). So the transcript
-- expires and the rest does not.
--
-- One year by default: long enough that nobody loses a recording they were
-- still going to read, short enough to bound the growth.
ALTER TABLE organizations
    -- 0 = keep forever. Spelled out because the number reads like the opposite.
    ADD COLUMN recording_retention_days INTEGER NOT NULL DEFAULT 365;

-- NULL = inherit the organisation's value. A number only ever extends it: an
-- agent may keep longer than the organisation requires, never shorter. The same
-- direction as recording_level, and for the same reason -- an agent that could
-- shorten its own trail is the gap the org-wide floor exists to close. That
-- rule lives in the query that deletes, not as a CHECK here: it is a statement
-- about the pair of values, not about either one.
ALTER TABLE agents
    ADD COLUMN recording_retention_days INTEGER;

-- The deletion asks "which runtime events of this agent are older than X". The
-- existing indexes lead with agent_id and id, which answers paging through a
-- recording and not this. Partial on the one kind that expires, so it stays
-- small: it covers a fifth of the rows today and none of the ones that stay.
CREATE INDEX idx_recording_runtime_alter
    ON recording_events (agent_id, created_at)
    WHERE kind = 'runtime';
