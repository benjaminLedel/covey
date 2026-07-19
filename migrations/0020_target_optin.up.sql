-- Zielsystem-Aktivierung wird opt-in (fail-closed): bisher galten kompilierte
-- Built-ins ohne Zeile als aktiviert — dadurch wirkten GitLab/Zammad überall
-- wie Standard (Aktionen, Webhooks, Profilfeld-Kennungen). Ab jetzt zählt nur
-- eine explizite Zeile mit enabled=TRUE. Bestandsschutz als Daten statt als
-- Code-Default: bestehende Organisationen bekommen ihre bisher implizit
-- aktiven Built-ins als explizite Aktivierung; wer deaktiviert hatte, behält
-- seine Zeile (ON CONFLICT DO NOTHING).
INSERT INTO target_plugins (org_id, name, kind, enabled)
SELECT o.id, b.name, 'builtin', TRUE
FROM organizations o
CROSS JOIN (VALUES ('zammad'), ('gitlab')) AS b(name)
ON CONFLICT (org_id, name) DO NOTHING;
