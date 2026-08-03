-- Fingerabdruck des Embedding-Modells je Wiki-Seite (spec/05).
--
-- Vektoren verschiedener Modelle sind untereinander nicht vergleichbar: eine
-- Kosinus-Ähnlichkeit zwischen einem Hash-Vektor und einem API-Vektor ist eine
-- Zufallszahl. Suche, Ingest-Zuordnung und Konsolidierung filtern deshalb auf
-- das aktuell konfigurierte Modell, und ReembedStale zieht die übrigen Seiten
-- im Hintergrund nach.
--
-- Bestand: alles, was es bisher gibt, stammt vom Built-in-Hash-Embedder.
ALTER TABLE wiki_pages ADD COLUMN embed_model TEXT NOT NULL DEFAULT '';
UPDATE wiki_pages SET embed_model = 'builtin-hash:256';

CREATE INDEX idx_wiki_pages_embed_model ON wiki_pages (agent_id, embed_model);
