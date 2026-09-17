-- Platform identifiers generically: instead of one column per target system
-- (gitlab_username) a JSONB map system → identifier ("identities").
-- Target systems are plugins without a hard-coded list — the profiles of the
-- employees follow the same principle: new platform, no schema change.
ALTER TABLE humans ADD COLUMN identities JSONB NOT NULL DEFAULT '{}'::jsonb;

UPDATE humans SET identities = jsonb_build_object('gitlab', gitlab_username)
    WHERE gitlab_username <> '';

ALTER TABLE humans DROP COLUMN gitlab_username;
