-- Aufgaben-Verwandtschaft: Eine Aufgabe kann aus einer anderen hervorgehen.
-- Zwei Fälle nutzen das:
--
--   * Fortsetzung — ein Lauf endete am Turn-Limit (max_turns) ohne Ergebnis.
--     Statt den nächsten Heartbeat bei null anfangen zu lassen, entsteht eine
--     Folgeaufgabe mit dem Übergabe-Stand als Auftrag und der Runtime-Session
--     des abgebrochenen Laufs zum Wiederaufsetzen.
--   * Teilaufgabe — der Agent zerlegt seine Arbeit selbst (covey/create_task).
--
-- Die Kette trägt den Loop-Schutz: über parent_task_id ist die Tiefe zählbar,
-- und eine Fortsetzungs-Kette bricht ab, statt endlos weiterzulaufen.
-- ON DELETE SET NULL: eine gelöschte Ursprungsaufgabe verwaist ihre Kinder,
-- löscht sie aber nicht — die Arbeit bleibt sichtbar.
ALTER TABLE backlog_tasks
    ADD COLUMN parent_task_id UUID REFERENCES backlog_tasks(id) ON DELETE SET NULL;

CREATE INDEX idx_backlog_tasks_parent ON backlog_tasks(parent_task_id);
