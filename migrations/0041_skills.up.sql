-- Skills: an agent's capabilities as their own object, not as prompt text.
--
-- So far every line of the agent config landed in the system prompt of EVERY
-- run (agents.CompilePrompt). For identity and boundaries that is right — not
-- for procedures: an agent with five playbooks pays for all five, even when
-- the run finds after three turns that there is nothing to do. Claude Code
-- knows skills for this: a skill directory under ~/.claude/skills/<name>/ is
-- always visible with its description, its body loads only on demand. Because
-- the daemon starts the run with HOME=<agent home>, skills stored there become
-- the PERSONAL skills of exactly this agent.
--
-- Two levels, built after the secrets model (0007):
--   agent_id IS NULL     → skill of the org library, linkable by every agent
--   agent_id IS NOT NULL → skill that belongs to this agent alone
CREATE TABLE skills (
    id          UUID PRIMARY KEY,
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id    UUID REFERENCES agents(id) ON DELETE CASCADE,
    -- name becomes the directory name and thereby the /slash-command. Hence kept
    -- tight: [a-z0-9-], validated in the application (internal/skills).
    name        TEXT NOT NULL,
    -- description is the only thing that stands in the context ALWAYS. It decides
    -- whether Claude loads the skill — so it stands here as a column and not in
    -- the file body: lists and UI need it without reading the files.
    description TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Uniqueness per level: an agent may have its own skill that is named exactly
-- like one from the library — when materialising, its own then wins (see
-- internal/skills.ForAgent).
CREATE UNIQUE INDEX uq_skills_org   ON skills (org_id, name) WHERE agent_id IS NULL;
CREATE UNIQUE INDEX uq_skills_agent ON skills (org_id, agent_id, name) WHERE agent_id IS NOT NULL;

-- A skill is a DIRECTORY, not a single file: SKILL.md plus optional extras
-- (reference tables, templates, examples) that the body refers to. That is
-- exactly what the feature lives on — the bulk sits in the additional files
-- and is read only when the skill really pulls.
--
-- path is relative to the skill directory. The application rejects absolute
-- paths and ".." before anything is written to disk.
CREATE TABLE skill_files (
    skill_id   UUID NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    path       TEXT NOT NULL,
    content    TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (skill_id, path)
);

-- Linking library skills to agents — the same rule as with secrets
-- (secret_assignments, see secrets/builtin.Resolve): without an assignment a
-- library entry reaches no agent. For skills the rule additionally carries the
-- cost side: every available skill keeps its description permanently in the
-- context, and a research agent does not need the deploy checklist. Explicit
-- instead of implicit.
CREATE TABLE skill_assignments (
    skill_id   UUID NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    agent_id   UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (skill_id, agent_id)
);

CREATE INDEX idx_skill_assignments_agent ON skill_assignments (agent_id);
