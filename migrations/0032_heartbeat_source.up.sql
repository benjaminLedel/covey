-- Heartbeats: Herkunft trennen. 'config' sind aus HEARTBEAT.md materialisierte
-- Einträge (verwaltet von SaveConfig), 'system' sind Plattform-Defaults, die die
-- Control Plane für alle Agenten setzt (z. B. der konfigurierbare Wiki-Aufräum-
-- Heartbeat, COVEY_WIKI_CLEANUP). Die Trennung erlaubt es, System-Defaults global
-- zu reconcilen, ohne agenteneigene Heartbeats anzufassen — und umgekehrt löscht
-- der Config-Sync nur noch 'config'-Zeilen. Ein agenteneigener HEARTBEAT.md-Eintrag
-- gleichen Namens gewinnt (der System-Default wird für diesen Agenten übersprungen).
ALTER TABLE agent_heartbeats ADD COLUMN source TEXT NOT NULL DEFAULT 'config';
