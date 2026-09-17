-- Fingerprint of the embedding model per wiki page (spec/05).
--
-- Vectors of different models are not comparable to one another: a cosine
-- similarity between a hash vector and an API vector is a random number.
-- Search, ingest assignment and consolidation therefore filter on the
-- currently configured model, and ReembedStale pulls the remaining pages
-- along in the background.
--
-- Existing stock: everything there is so far comes from the built-in hash embedder.
ALTER TABLE wiki_pages ADD COLUMN embed_model TEXT NOT NULL DEFAULT '';
UPDATE wiki_pages SET embed_model = 'builtin-hash:256';

CREATE INDEX idx_wiki_pages_embed_model ON wiki_pages (agent_id, embed_model);
