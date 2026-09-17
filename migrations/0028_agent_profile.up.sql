-- Agent profile: the same profile fields as for people (0017–0019).
-- Agents are employees (spec/02) — function, contact, platform identifiers
-- and the org-wide configurable fields (profile_fields → custom) apply
-- to them in the same way. The values appear in the org chart and are
-- queryable by other agents via the covey/org_chart action.
ALTER TABLE agents
    ADD COLUMN job_title        TEXT NOT NULL DEFAULT '',
    ADD COLUMN identities       JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN phone            TEXT NOT NULL DEFAULT '',
    ADD COLUMN responsibilities TEXT NOT NULL DEFAULT '',
    ADD COLUMN custom           JSONB NOT NULL DEFAULT '{}'::jsonb;
