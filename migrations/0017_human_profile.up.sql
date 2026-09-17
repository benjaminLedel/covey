-- Employee profile: contact data and target system identifiers of the humans.
-- This lets agents know who on the team is responsible for what and under which
-- identifier a person is reachable in target systems (e.g. GitLab username
-- for assigning an issue to someone for testing).
ALTER TABLE humans
    ADD COLUMN job_title        TEXT NOT NULL DEFAULT '',
    ADD COLUMN gitlab_username  TEXT NOT NULL DEFAULT '',
    ADD COLUMN phone            TEXT NOT NULL DEFAULT '',
    ADD COLUMN responsibilities TEXT NOT NULL DEFAULT '';
