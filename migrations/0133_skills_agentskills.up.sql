-- Skills follow the Agent Skills format (https://agentskills.io/specification).
--
-- 0041 kept only name and description and rejected every other frontmatter key,
-- so a skill carrying `license:` — most published ones do — could not be
-- imported at all. The four optional keys of the specification get columns of
-- their own rather than one JSON blob: each has a meaning covey renders back into
-- the SKILL.md, and a typed column says which ones exist. Keys outside the
-- specification stay rejected in the application (internal/skills).
--
-- allowed_tools is written back verbatim. It pre-approves tools for the runtime;
-- it cannot widen what a run has, because the run's tool scope (--tools,
-- --allowedTools) is set by covey outside the skill.
ALTER TABLE skills
    ADD COLUMN license       TEXT  NOT NULL DEFAULT '',
    ADD COLUMN compatibility TEXT  NOT NULL DEFAULT '',
    ADD COLUMN allowed_tools TEXT  NOT NULL DEFAULT '',
    ADD COLUMN metadata      JSONB NOT NULL DEFAULT '{}';

-- A skill's assets/ may hold images, fonts or spreadsheet templates. TEXT cannot
-- carry them (Postgres rejects NUL bytes and invalid UTF-8), so a binary file
-- keeps its bytes in data and leaves content empty. data IS NULL = text file,
-- which is every row written before this migration.
ALTER TABLE skill_files ADD COLUMN data BYTEA;
