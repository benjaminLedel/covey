-- Mitarbeiter-Profil: Kontaktdaten und Zielsystem-Kennungen der Menschen.
-- Damit wissen Agenten, wer im Team wofür zuständig ist und unter welcher
-- Kennung eine Person in Zielsystemen erreichbar ist (z. B. GitLab-Username
-- für die Zuweisung eines Issues zum Testen).
ALTER TABLE humans
    ADD COLUMN job_title        TEXT NOT NULL DEFAULT '',
    ADD COLUMN gitlab_username  TEXT NOT NULL DEFAULT '',
    ADD COLUMN phone            TEXT NOT NULL DEFAULT '',
    ADD COLUMN responsibilities TEXT NOT NULL DEFAULT '';
