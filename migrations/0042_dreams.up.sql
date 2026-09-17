-- Dreams (spec/05): the agent tidies its memory at night — it merges
-- duplicates, renames diary titles into entity titles, and later more
-- (link, classify, clear away what decayed). So far this pass ran
-- transiently: duplicate consolidation in the 10-minute ticker left only
-- one line in wiki_log, the title pass nothing lasting at all.
--
-- That is not enough once the run writes unattended at night. Whoever wants
-- to see in the morning what happened to their agent's memory during the
-- night needs two things: the dream as a whole (when, how long, whether it
-- completed) and every single change with its before — otherwise
-- "undoable" is no more than a word.
--
-- Deliberately separate from wiki_log: that records individual writes to the
-- wiki, regardless of who triggered them. A dream is the bracket above them
-- and carries intent, cost and context.
CREATE TABLE dreams (
    id          UUID PRIMARY KEY,
    agent_id    UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    -- manual | nightly — where the dream came from. Stands in the UI, because
    -- it makes a difference whether somebody was watching.
    trigger     TEXT NOT NULL DEFAULT 'manual',
    -- running | done | error. A dream left stuck by a restart stays on
    -- running; that is more honest than completing it silently, and the start
    -- endpoint clears it after a grace period.
    status      TEXT NOT NULL DEFAULT 'running',
    error       TEXT NOT NULL DEFAULT '',
    -- Phase of the running dream (merge | titles | …) for the progress display.
    phase       TEXT NOT NULL DEFAULT '',
    -- How many pages the dream looked at, and how many it did not reach (cap
    -- per run) — both belong in the record, so that a dream does not look
    -- more complete than it was.
    looked_at   INT NOT NULL DEFAULT 0,
    skipped     INT NOT NULL DEFAULT 0,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ
);
CREATE INDEX idx_dreams_agent ON dreams (agent_id, started_at DESC);

-- A single action inside a dream. `before` carries the state from before and
-- is the basis for undoing; `undone_at` records that somebody made use of
-- that right instead of deleting the row — a retracted dream step is itself
-- a piece of information.
CREATE TABLE dream_actions (
    id         UUID PRIMARY KEY,
    dream_id   UUID NOT NULL REFERENCES dreams(id) ON DELETE CASCADE,
    -- retitle | merge (later: link, classify, prune)
    kind       TEXT NOT NULL,
    page_slug  TEXT NOT NULL DEFAULT '',
    before     TEXT NOT NULL DEFAULT '',
    after      TEXT NOT NULL DEFAULT '',
    -- The model's justification, in its own words. Without it a rename is a
    -- claim nobody can retrace.
    reason     TEXT NOT NULL DEFAULT '',
    undone_at  TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_dream_actions_dream ON dream_actions (dream_id, created_at);
