-- Model per agent (e.g. claude-opus-4-8, claude-sonnet-5). Empty = the
-- runtime decides itself (default of the binary or the account).
ALTER TABLE agents ADD COLUMN model TEXT NOT NULL DEFAULT '';
