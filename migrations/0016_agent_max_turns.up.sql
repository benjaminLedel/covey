-- Turn limit per agent for one runtime run (runaway guard). 0 = default
-- of the orchestrator (30). Applies like model at the next task dispatch.
ALTER TABLE agents ADD COLUMN max_turns INTEGER NOT NULL DEFAULT 0;
