-- Seitentyp und Tags (spec/05): Das Wiki-Modell verlangt "eine Seite pro
-- Entität — Kunde, Projekt, Kollege, System, wiederkehrendes Problem". Bisher
-- trug keine Seite diese Information: das Frontmatter kannte nur `title`, alles
-- andere verwarf der Home-Sync. Ohne Typ bleibt das Wiki eine flache Liste, in
-- der sich weder navigieren noch erkennen lässt, ob ein Agent Entitätsseiten
-- pflegt oder Tagebuch führt.
--
-- Leerer Typ heißt "nicht zugeordnet" und ist ein Qualitätsbefund, kein Fehler:
-- Bestandsseiten haben ihn, bis jemand (Agent oder Mensch) sie einsortiert.
ALTER TABLE wiki_pages ADD COLUMN type TEXT NOT NULL DEFAULT '';
ALTER TABLE wiki_pages ADD COLUMN tags TEXT[] NOT NULL DEFAULT '{}';

CREATE INDEX idx_wiki_pages_type ON wiki_pages (agent_id, type);
