-- Voices: an author's style as an object of the organisation (spec/24).
--
-- Why a table and not a file in the agent config: a voice belongs to the
-- ORGANISATION, several agents carry the same one, and it is built from texts
-- somebody uploaded once. A copy per agent would drift apart at the first
-- rebuild, and the corpus would have nowhere to live.
--
-- What an agent carries stays a file: assigning a voice writes TONE.md into the
-- agent's config, which is versioned, reviewable and revertible like the rest of
-- it (spec/02). The table holds the source, the file holds what acts.
CREATE TABLE voices (
    id         UUID PRIMARY KEY,
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    -- One voice per language and register. A corpus of seven blog posts and one
    -- legal notice gives bands that fit neither, so the language is a property
    -- of the voice rather than something resolved per text.
    language   TEXT NOT NULL DEFAULT '',
    -- version counts the BUILDS. A rebuild after a metric change or a larger
    -- corpus is a new version of the same voice, and the agents that carry it
    -- keep working until somebody re-assigns it.
    version    INT  NOT NULL DEFAULT 0,
    -- The three deterministic artefacts, as the build produced them.
    profile    JSONB NOT NULL DEFAULT '{}'::jsonb,
    exemplars  JSONB NOT NULL DEFAULT '[]'::jsonb,
    contrast   JSONB NOT NULL DEFAULT '[]'::jsonb,
    notes      JSONB NOT NULL DEFAULT '[]'::jsonb,
    -- The card is the one artefact a model writes, so it is the one a person
    -- releases. card holds the draft; released_card is what acts, and a build
    -- does not overwrite it — the corpus may grow, the description somebody
    -- signed stays until they change it.
    card          TEXT NOT NULL DEFAULT '',
    released_card TEXT NOT NULL DEFAULT '',
    released_at   TIMESTAMPTZ,
    released_by   UUID REFERENCES humans(id) ON DELETE SET NULL,
    words      INT NOT NULL DEFAULT 0,
    documents  INT NOT NULL DEFAULT 0,
    built_at   TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The name is how a person picks it in the agent settings, so it has to be
-- unambiguous within the organisation.
CREATE UNIQUE INDEX idx_voices_org_name ON voices (org_id, lower(name));

-- The corpus stays. Not for nostalgia: a rebuild after a metric change has to
-- run on the same texts, otherwise "the same corpus gives the same profile" is
-- not a property anybody can check.
CREATE TABLE voice_documents (
    id         UUID PRIMARY KEY,
    voice_id   UUID NOT NULL REFERENCES voices(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    body       TEXT NOT NULL,
    words      INT  NOT NULL DEFAULT 0,
    -- 'author' is the corpus the voice is built from. 'reference' is the
    -- opposite pole: AI text, against which the contrast list is measured —
    -- "what this author never does" is a comparison, and without the other
    -- side there is nothing to compare with.
    --
    -- It is uploaded rather than shipped. A reference corpus nobody measured
    -- would be a number invented with a straight face, and the organisation
    -- that has AI drafts of its own has the honest one: its agents wrote them.
    kind       TEXT NOT NULL DEFAULT 'author' CHECK (kind IN ('author','reference')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_voice_documents_voice ON voice_documents (voice_id, created_at);

-- Which voice an agent carries. The TONE.md in its config is what acts; this
-- column is what lets the platform say WHOSE voice that is — for the library
-- page, for a rebuild that wants to know whom it concerns, and for the
-- correction pairs of slice 3.
ALTER TABLE agents ADD COLUMN voice_id UUID REFERENCES voices(id) ON DELETE SET NULL;
