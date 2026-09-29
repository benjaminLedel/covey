-- Voices per occasion, chosen by who is spoken to (#471).
--
-- An agent carried exactly one voice (agents.voice_id, 0090), and it acted on
-- everything the agent wrote outward. One voice cannot fit both a customer
-- mail and a quick answer to a colleague, and an organisation could not say
-- how each of its departments wants to be spoken to. So a voice is now
-- assigned per OCCASION:
--
--   chat          the team chat (the triage and the narration, and the runs
--                 of tasks that came from a conversation)
--   customers     outward: mails, tickets, replies in target systems
--   publications  blog, docs, offers
--
-- and at three levels, of which the first that applies wins when something is
-- written (internal/voice/occasion.go): the addressed person's department ×
-- occasion, the agent × occasion, the organisation × occasion.
--
-- One table for the three levels rather than three tables: the question every
-- reader asks is "which voice, for whom, for what", and the voice page asks it
-- from the voice's side ("used by"). agent_id and department_id carry foreign
-- keys, so a deleted agent or department takes its slots with it; a row with
-- neither is the organisation's default. ON DELETE CASCADE on the voice: a
-- deleted voice leaves an empty slot, and an empty slot falls back to the next
-- level — never to a voice that no longer exists.
CREATE TABLE voice_assignments (
    org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id      UUID REFERENCES agents(id) ON DELETE CASCADE,
    department_id UUID REFERENCES departments(id) ON DELETE CASCADE,
    occasion      TEXT NOT NULL CHECK (occasion IN ('chat', 'customers', 'publications')),
    voice_id      UUID NOT NULL REFERENCES voices(id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (agent_id IS NULL OR department_id IS NULL)
);
-- One voice per holder and occasion.
CREATE UNIQUE INDEX idx_voice_assignments_agent ON voice_assignments (agent_id, occasion)
    WHERE agent_id IS NOT NULL;
CREATE UNIQUE INDEX idx_voice_assignments_department ON voice_assignments (department_id, occasion)
    WHERE department_id IS NOT NULL;
CREATE UNIQUE INDEX idx_voice_assignments_org ON voice_assignments (org_id, occasion)
    WHERE agent_id IS NULL AND department_id IS NULL;
-- "Used by" on the voice page.
CREATE INDEX idx_voice_assignments_voice ON voice_assignments (voice_id);

-- The one voice an agent carried goes into BOTH outward slots: it was built
-- for what the agent writes into target systems, and that is what customers
-- and publications are. The chat slot stays empty on purpose. Until now the
-- team chat read only the voice's chat tone (0120), never its card or its
-- passages; an empty slot falls back to the organisation's default for the
-- chat, and with none set the chat reads the organisation's chat tone as
-- before — so nobody's colleague suddenly starts sounding like a tender.
INSERT INTO voice_assignments (org_id, agent_id, occasion, voice_id)
SELECT a.org_id, a.id, o.occasion, a.voice_id
  FROM agents a CROSS JOIN (VALUES ('customers'), ('publications')) AS o(occasion)
 WHERE a.voice_id IS NOT NULL;

-- The chat tone (0120) belonged to the voice an agent carried, and it keeps
-- acting in the chat through this migration: with the chat slot empty, the
-- tone is read from the agent's customers voice (voice.Store.ChatToneFor).
--
-- agents.voice_id stays as a column for now. Nothing reads it any more — the
-- slots above are the assignment — and the one-voice endpoint still writes it,
-- so that a binary rolled back past this migration finds what it expects.
-- Dropping it is a later migration's job.

-- How a department wants to be spoken to: one short line, at most 400
-- characters (org.AudienceNoteMax; the column checks it too). Sales wants what it means for the
-- customer, engineering the ticket and the branch, management the result
-- first. It is added to whatever an agent writes to somebody of that
-- department, whichever voice applies.
ALTER TABLE departments ADD COLUMN audience_note TEXT NOT NULL DEFAULT ''
    CHECK (char_length(audience_note) <= 400);
