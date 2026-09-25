-- The notetaker (#336, spec/14): what a person captures for themselves.
--
-- A note belongs to a seat (human_id) and to nobody else — no role reads
-- another person's notes, not even org_admin. Three kinds in one list: a
-- typed note, a voice note and a meeting. Voice and meeting arrive as text:
-- speech is recognised on the device and no audio reaches covey.
--
-- summary is what "Summarise" wrote for a meeting (a control-plane turn with
-- the organisation's credential); empty until somebody asks for it.
CREATE TABLE human_notes (
    id               uuid PRIMARY KEY,
    org_id           uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    human_id         uuid NOT NULL REFERENCES humans(id) ON DELETE CASCADE,
    kind             text NOT NULL CHECK (kind IN ('text', 'voice', 'meeting')),
    title            text NOT NULL DEFAULT '',
    body             text NOT NULL,
    summary          text NOT NULL DEFAULT '',
    duration_seconds integer NOT NULL DEFAULT 0 CHECK (duration_seconds >= 0),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- Read as one person's list, newest first.
CREATE INDEX idx_human_notes_owner ON human_notes (human_id, created_at DESC);
