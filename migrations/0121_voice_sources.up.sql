-- Where a voice comes from (#458). Until now a voice could only be measured
-- from uploaded texts; it can now also be described in words, and an agent can
-- draft one for a person to release.
--
-- source: 'texts' is the measured voice of spec/24 — profile, exemplars and
-- contrast from a corpus. 'described' was written by the organisation's model
-- from a description in plain words: card, exemplars and a suggested chat tone,
-- and NO profile — nothing was measured, so the style gate has nothing to
-- check against. A build from texts turns a described voice into a measured one.
ALTER TABLE voices ADD COLUMN source TEXT NOT NULL DEFAULT 'texts'
    CHECK (source IN ('texts', 'described'));
-- What the voice is for: blog, support_mail, chat, offers, other. It goes into
-- the prompts that write for the voice; validated in internal/voice.
ALTER TABLE voices ADD COLUMN purpose TEXT NOT NULL DEFAULT '';
-- The description a described voice was written from, kept so a person can
-- read what the card rests on and write it again.
ALTER TABLE voices ADD COLUMN description TEXT NOT NULL DEFAULT '';
-- Exemplars a model wrote wait for the release like the card does: they are a
-- claim about a hand, not passages somebody wrote. Measured exemplars do not
-- pass through here — they are quotes from the corpus.
ALTER TABLE voices ADD COLUMN draft_exemplars JSONB NOT NULL DEFAULT '[]';
-- The chat tone the description suggests. A suggestion, not the tone: the
-- chat tone acts as soon as it is set (0120), so a person saves it.
ALTER TABLE voices ADD COLUMN suggested_chat_tone JSONB NOT NULL DEFAULT '{}';
-- The agent that drafted the voice (covey/voice_draft), for the page to say
-- so. A draft acts nowhere until a person releases it.
ALTER TABLE voices ADD COLUMN drafted_by UUID REFERENCES agents(id) ON DELETE SET NULL;
