-- Turn-Limit je Agent für einen Runtime-Lauf (Runaway-Guard). 0 = Default
-- des Orchestrators (30). Greift wie model beim nächsten Task-Dispatch.
ALTER TABLE agents ADD COLUMN max_turns INTEGER NOT NULL DEFAULT 0;
