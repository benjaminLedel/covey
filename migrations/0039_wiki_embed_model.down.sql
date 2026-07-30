DROP INDEX IF EXISTS idx_wiki_pages_embed_model;
ALTER TABLE wiki_pages DROP COLUMN IF EXISTS embed_model;
