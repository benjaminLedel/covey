-- Heartbeats: Signatur des zuletzt gemeldeten Arbeitsvorrats. Eine
-- nur-wenn:-Bedingung ist pegel-, nicht flankengesteuert — sie meldet „dort
-- wartet Arbeit", solange der Zustand besteht. Ein Agent, der einen Lauf
-- BEWUSST kommentarlos beendet (etwa weil die Rückmeldung des QA-Kollegen nur
-- eine Freigabe war), würde deshalb im nächsten Intervall erneut auf dieselbe
-- Sache geweckt — und kommentiert am Ende nur noch, um seinen eigenen Wecker
-- abzustellen. Mit der Signatur feuert der Heartbeat erst wieder, wenn sich der
-- Arbeitsvorrat tatsächlich GEÄNDERT hat (neuer Kommentar, neuer MR). Schweigen
-- wird damit eine gültige Antwort.
ALTER TABLE agent_heartbeats ADD COLUMN last_work_sig TEXT NOT NULL DEFAULT '';
