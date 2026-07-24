-- Heartbeats: optionale Feuer-Bedingung nur-wenn: <system>. Vor dem Anlegen
-- der Backlog-Aufgabe fragt die Control Plane das Zielsystem-Plugin, ob
-- Arbeit vorliegt (z. B. ungelesene Mails per IMAP) — liegt keine vor,
-- entfällt der Lauf und damit der (teure) Agenten-Wake. Leer = immer feuern.
ALTER TABLE agent_heartbeats ADD COLUMN only_if TEXT NOT NULL DEFAULT '';
