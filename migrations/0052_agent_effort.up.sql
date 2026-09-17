-- Reasoning effort per agent (low, medium, high, xhigh, max — Claude Code's
-- `--effort`). Empty = the runtime decides for itself (default of the binary).
ALTER TABLE agents ADD COLUMN effort TEXT NOT NULL DEFAULT '';
