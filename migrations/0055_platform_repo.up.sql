-- Where the source code of this platform lives — as configuration, not as
-- a decision made by an agent (spec/21).
--
-- The operations engineer files the most valuable finding where he cannot fix
-- it with a config: three agents died on the turn limit this week, none was
-- misconfigured — the platform lacks a way to return a partial result. That is
-- a bug report, and he is the only one in the organisation who has seen all
-- three of them.
--
-- WHICH repository is decided by the organisation, not by the agent. An
-- instance that runs against the public GitHub mirror would otherwise have an
-- agent writing issues where the whole world reads along; an instance that
-- reports into its own GitLab keeps them in the house. Hence master data
-- beside the org description and no prompt text: whatever an agent may choose
-- itself, it chooses differently someday.
--
-- The same address carries both — reading and reporting. Anyone reporting
-- against code he has not read reports symptoms; anyone reading a revision
-- other than the one that runs reports half of it twice and the other half
-- too early.
ALTER TABLE organizations
    -- The target system that is read from and reported to: the name of the
    -- plugin as it stands in an ACCESS.md ('gitlab', 'github').
    -- Empty = not configured, and then nothing of it stands in the prompt.
    ADD COLUMN platform_repo_system TEXT NOT NULL DEFAULT '',
    -- The project there, in the spelling of the system: with GitLab the
    -- numeric ID or the path, with GitHub "owner/repo".
    ADD COLUMN platform_repo_project TEXT NOT NULL DEFAULT '';
