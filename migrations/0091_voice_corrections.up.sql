-- Correction pairs: what a person changed about an agent's text (spec/24).
--
-- The strongest signal for imitation is not a rule and not a band, it is a
-- pair: this is what the agent wrote, this is what a person made of it. A model
-- learns a transformation it can see better than one it is told about, and a
-- voice that collects pairs gets more exact with every correction — without
-- anybody maintaining a corpus.
--
-- They belong to the VOICE, not to the agent: several agents carry one voice,
-- and a correction is about the hand, not about who was holding the pen.
CREATE TABLE voice_corrections (
    id       UUID PRIMARY KEY,
    voice_id UUID NOT NULL REFERENCES voices(id) ON DELETE CASCADE,
    -- The agent whose text it was. Kept for the trail, and nullable because a
    -- correction outlives the agent that occasioned it.
    agent_id UUID REFERENCES agents(id) ON DELETE SET NULL,
    -- Where the pair came from: 'approval' (a reviewer rewrote the text at the
    -- gate) and later the target systems, where somebody edits a published
    -- text. The column exists from the first day because the second source is
    -- the one the design is actually about, and a pair without its origin
    -- cannot be weighed.
    source   TEXT NOT NULL,
    -- The action the text was going into ("gitlab:comment", "mail:send"),
    -- empty where there was none. Register is not style: a correction on a
    -- mail says something different from one on a commit message.
    action   TEXT NOT NULL DEFAULT '',
    -- The pair itself.
    before_text TEXT NOT NULL,
    after_text  TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  UUID REFERENCES humans(id) ON DELETE SET NULL
);
CREATE INDEX idx_voice_corrections_voice ON voice_corrections (voice_id, created_at DESC);
