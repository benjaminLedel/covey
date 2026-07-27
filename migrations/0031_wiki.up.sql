-- Wiki-Gedächtnis (spec/05): das semantische Gedächtnis wird von losen
-- Freitext-Schnipseln zu verlinkten Markdown-Seiten + pgvector-Index. Die Seite
-- ist die Einheit, Wikilinks tragen die Beziehungen (statt eines Graph-Stores).
-- Der alte flache Store (memories, 0002) bleibt als Bestandsdaten erhalten und
-- wird hier seitenweise übernommen.

CREATE TABLE wiki_pages (
    id         UUID PRIMARY KEY,
    agent_id   UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    slug       TEXT NOT NULL,
    title      TEXT NOT NULL,
    body       TEXT NOT NULL DEFAULT '',
    links      TEXT[] NOT NULL DEFAULT '{}',   -- Wikilinks: slugs verwandter Seiten
    scope      TEXT NOT NULL DEFAULT 'agent',  -- agent | org (D5, spec/07)
    source     TEXT NOT NULL DEFAULT 'agent',  -- agent | manual
    metadata   JSONB NOT NULL DEFAULT '{}',
    embedding  vector(256) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (agent_id, slug)
);
CREATE INDEX idx_wiki_pages_agent ON wiki_pages (agent_id, updated_at DESC);

-- log.md als Tabelle: chronologisches Protokoll aller Wiki-Operationen.
CREATE TABLE wiki_log (
    id         BIGSERIAL PRIMARY KEY,
    agent_id   UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    op         TEXT NOT NULL,          -- ingest | write | merge | delete
    page_slug  TEXT,
    summary    TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_wiki_log_agent ON wiki_log (agent_id, created_at DESC);

-- Bestandsübernahme: jeder alte Schnipsel wird eine eigene Seite. Der Titel ist
-- der auf 80 Zeichen gekürzte, normalisierte Inhalt; der Slug leitet sich aus
-- der ID ab (kollisionsfrei).
INSERT INTO wiki_pages (id, agent_id, slug, title, body, embedding, source, metadata, created_at, updated_at)
SELECT id, agent_id,
       'ep-' || left(replace(id::text, '-', ''), 12),
       left(trim(regexp_replace(content, '\s+', ' ', 'g')), 80),
       content, embedding,
       COALESCE(metadata->>'source', 'agent'),
       metadata, created_at, created_at
FROM memories;
