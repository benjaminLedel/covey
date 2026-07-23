ALTER TABLE humans ADD COLUMN gitlab_username TEXT NOT NULL DEFAULT '';

UPDATE humans SET gitlab_username = COALESCE(identities->>'gitlab', '');

ALTER TABLE humans DROP COLUMN identities;
