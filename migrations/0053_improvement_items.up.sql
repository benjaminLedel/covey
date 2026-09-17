-- The proposal: a stored config version that is NOT in force.
--
-- agent_config_versions numbers per agent and treats the highest number as the
-- truth — there is no version there that lies around without running. That is
-- exactly what the operations engineer needs (spec/21): it reads the work
-- record of a colleague and proposes a change, which only takes effect once a
-- human accepts it. That is why a proposal stands in its own table and not
-- with a flag in the version sequence: what stands there, runs.
--
-- Deliberately one table for THREE outcomes and not just for the proposal.
-- A review ends in one of three diagnoses (spec/21): the config is
-- wrong -> a proposal with a diff; the assignment is wrong -> a finding to
-- the human who owns it, without a diff; the platform is wrong -> an
-- issue that already lies in the tracker. All three need the same person
-- and the same inbox. The platform has no message to a human, and to
-- invent one for this feature would mean placing a second, worse inbox
-- beside the one that has to exist anyway.
CREATE TABLE improvement_items (
    id            UUID PRIMARY KEY,
    org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    -- The colleague this is about. Not the sender: that stands in
    -- author_agent_id, and the two may never be the same one (spec/21,
    -- "it does not review itself").
    agent_id      UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN ('proposal','finding','issue')),
    title         TEXT NOT NULL,
    -- The justification: why, from which observation. This is the text a
    -- human reads before they read the diff.
    rationale     TEXT NOT NULL DEFAULT '',

    -- Only for a proposal: the inactive version.
    -- base_version is the version written AGAINST. It turns the proposal into
    -- a diff with a base — if the agent is changed by hand in the meantime, an
    -- acceptance does not silently overwrite that change, it
    -- shows the conflict. The same case as with a pull request,
    -- and the same answer. 0 = no proposal.
    base_version  INTEGER NOT NULL DEFAULT 0,
    -- Only the files the proposal CHANGES — not the whole set.
    -- Acceptance merges, never replaces: what is missing here stays.
    files         JSONB NOT NULL DEFAULT '{}'::jsonb,

    -- Provenance, written by the platform and not reported by the model.
    -- NULL on the author = a human created the item.
    author_agent_id UUID REFERENCES agents(id) ON DELETE SET NULL,
    task_id         UUID REFERENCES backlog_tasks(id) ON DELETE SET NULL,

    -- The decision. A rejected proposal stays, along with the
    -- reason: it is the most useful thing someone can read who wants to review
    -- the operations engineer themselves.
    status        TEXT NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending','accepted','rejected')),
    decided_by    UUID REFERENCES humans(id) ON DELETE SET NULL,
    decided_at    TIMESTAMPTZ,
    decision_note TEXT NOT NULL DEFAULT '',
    -- The version that came out of the acceptance (0 = none). It is created on
    -- the normal write path, with the human as created_by.
    applied_version INTEGER NOT NULL DEFAULT 0,

    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The query of the list: open items of an organisation, newest first.
CREATE INDEX idx_improvement_org_status ON improvement_items (org_id, status, created_at DESC);
-- And the second view: what is pending for this colleague (employee profile).
CREATE INDEX idx_improvement_agent ON improvement_items (agent_id, created_at DESC);
