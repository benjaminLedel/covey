-- Page type and tags (spec/05): the wiki model asks for "one page per
-- entity — customer, project, colleague, system, recurring problem". So far
-- no page carried this information: the frontmatter knew only `title`, the
-- home sync threw everything else away. Without a type the wiki stays a flat
-- list, in which neither navigation nor the question of whether an agent
-- maintains entity pages or keeps a diary is answerable.
--
-- An empty type means "not assigned" and is a quality finding, not an error:
-- existing pages have it until someone (agent or human) files them.
ALTER TABLE wiki_pages ADD COLUMN type TEXT NOT NULL DEFAULT '';
ALTER TABLE wiki_pages ADD COLUMN tags TEXT[] NOT NULL DEFAULT '{}';

CREATE INDEX idx_wiki_pages_type ON wiki_pages (agent_id, type);
