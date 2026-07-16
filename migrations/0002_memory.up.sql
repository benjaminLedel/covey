-- M7: episodisches Gedächtnis über pgvector.
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE memories (
    id         UUID PRIMARY KEY,
    agent_id   UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    content    TEXT NOT NULL,
    embedding  vector(256) NOT NULL,
    metadata   JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_memories_agent ON memories (agent_id, created_at);
