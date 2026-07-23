-- Plattform-Kennungen generisch: statt einer Spalte pro Zielsystem
-- (gitlab_username) eine JSONB-Map system → kennung ("identities").
-- Zielsysteme sind Plugins ohne hartkodierte Liste — die Profile der
-- Mitarbeiter folgen demselben Prinzip: neue Plattform, kein Schema-Change.
ALTER TABLE humans ADD COLUMN identities JSONB NOT NULL DEFAULT '{}'::jsonb;

UPDATE humans SET identities = jsonb_build_object('gitlab', gitlab_username)
    WHERE gitlab_username <> '';

ALTER TABLE humans DROP COLUMN gitlab_username;
