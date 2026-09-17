-- Recording depth (recording profile, spec/06): the org floor applies to all
-- agents (security/compliance), the agent override can only go DEEPER (never below
-- the floor). Levels: minimal < standard < full (full = incl. screenshots).
ALTER TABLE organizations ADD COLUMN recording_level text NOT NULL DEFAULT 'standard';

-- NULL = inherits the org floor; a set value tightens it (effective = max).
ALTER TABLE agents ADD COLUMN recording_level text;
