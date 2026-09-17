-- The review: what operations has written about a colleague, dated.
--
-- Deliberately NOT in improvement_items. An open item waits for the
-- decision of a human and then leaves the pool; a review waits for
-- nothing. It is the file beside the file: the page someone
-- opens when they want to know what is up with an agent — with
-- a story instead of a number without one (spec/21).
--
-- The link to the items that came out of it runs over the
-- TASK and not over a foreign key: both arise in the same
-- run, carry the same task_id, and a second link would be a
-- second truth that can drift apart.
CREATE TABLE agent_reviews (
    id              UUID PRIMARY KEY,
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    -- The reviewed colleague.
    agent_id        UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    -- Who wrote it. NULL when the agent is deleted later —
    -- the review stays anyway.
    author_agent_id UUID REFERENCES agents(id) ON DELETE SET NULL,
    task_id         UUID REFERENCES backlog_tasks(id) ON DELETE SET NULL,
    -- The period that was judged over. Without it, "eight aborts"
    -- is no statement.
    period_from     TIMESTAMPTZ NOT NULL,
    period_to       TIMESTAMPTZ NOT NULL,
    -- The text. Markdown, like everything a human reads here.
    summary         TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The one query: the history of a colleague, newest first.
CREATE INDEX idx_agent_reviews_agent ON agent_reviews (agent_id, created_at DESC);

-- And the address of an issue that already lies in the tracker. A report without
-- the link there forces every reader to search.
ALTER TABLE improvement_items ADD COLUMN link TEXT NOT NULL DEFAULT '';
