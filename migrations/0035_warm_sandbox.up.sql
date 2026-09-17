-- Warm sandbox (opt-in per agent): keeps the sandbox live between wake phases,
-- instead of tearing it down when the agent sleeps. The dev server and caches
-- (node_modules, build) survive — for agents like the QA tester, that otherwise
-- set up from scratch on every run. Default false: all other agents stay
-- ephemeral ("dumb and replaceable", spec/01).
ALTER TABLE agents ADD COLUMN warm_sandbox boolean NOT NULL DEFAULT false;
