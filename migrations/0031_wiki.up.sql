-- Wiki memory (spec/05): the semantic memory turns from loose free-text
-- snippets into linked Markdown pages + pgvector index. The page
-- is the unit, wikilinks carry the relationships (instead of a graph store).
-- The old flat store (memories, 0002) stays as existing data and
-- is carried over page by page here.

CREATE TABLE wiki_pages (
    id         UUID PRIMARY KEY,
    agent_id   UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    slug       TEXT NOT NULL,
    title      TEXT NOT NULL,
    body       TEXT NOT NULL DEFAULT '',
    links      TEXT[] NOT NULL DEFAULT '{}',   -- Wikilinks: slugs of related pages
    scope      TEXT NOT NULL DEFAULT 'agent',  -- agent | org (D5, spec/07)
    source     TEXT NOT NULL DEFAULT 'agent',  -- agent | manual
    metadata   JSONB NOT NULL DEFAULT '{}',
    embedding  vector(256) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (agent_id, slug)
);
CREATE INDEX idx_wiki_pages_agent ON wiki_pages (agent_id, updated_at DESC);

-- log.md as a table: chronological log of all wiki operations.
CREATE TABLE wiki_log (
    id         BIGSERIAL PRIMARY KEY,
    agent_id   UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    op         TEXT NOT NULL,          -- ingest | write | merge | delete
    page_slug  TEXT,
    summary    TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_wiki_log_agent ON wiki_log (agent_id, created_at DESC);

-- Carry-over: every old snippet becomes a page of its own. The title is
-- the content normalised and cut to 80 characters; the slug derives from
-- the ID (collision-free).
INSERT INTO wiki_pages (id, agent_id, slug, title, body, embedding, source, metadata, created_at, updated_at)
SELECT id, agent_id,
       'ep-' || left(replace(id::text, '-', ''), 12),
       left(trim(regexp_replace(content, '\s+', ' ', 'g')), 80),
       content, embedding,
       COALESCE(metadata->>'source', 'agent'),
       metadata, created_at, created_at
FROM memories;
