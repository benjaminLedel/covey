-- Agenten-Profil: dieselben Profilfelder wie bei den Menschen (0017–0019).
-- Agenten sind Mitarbeiter (spec/02) — Funktion, Kontakt, Plattform-Kennungen
-- und die org-weit konfigurierbaren Felder (profile_fields → custom) gelten
-- für sie genauso. Die Werte erscheinen im Org-Chart und sind für andere
-- Agenten über die covey/org_chart-Aktion abfragbar.
ALTER TABLE agents
    ADD COLUMN job_title        TEXT NOT NULL DEFAULT '',
    ADD COLUMN identities       JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN phone            TEXT NOT NULL DEFAULT '',
    ADD COLUMN responsibilities TEXT NOT NULL DEFAULT '',
    ADD COLUMN custom           JSONB NOT NULL DEFAULT '{}'::jsonb;
