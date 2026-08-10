-- DROP INDEX steht hier noch: Entwicklungsdatenbanken, auf denen eine fruehere
-- Fassung dieser Migration lief, tragen agents_hired_idx noch.
DROP INDEX IF EXISTS agents_hired_idx;
ALTER TABLE agents DROP COLUMN IF EXISTS hired_at;
