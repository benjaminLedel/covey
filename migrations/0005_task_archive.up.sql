-- Aufräumen im Backlog: terminale Aufgaben (done/failed/cancelled) lassen sich
-- archivieren statt für immer das Board zu füllen. Archiviert = ausgeblendet,
-- aber vollständig erhalten (Historie, Recording-Verweise bleiben gültig).
ALTER TABLE backlog_tasks ADD COLUMN archived_at timestamptz;

CREATE INDEX idx_backlog_tasks_active ON backlog_tasks (agent_id) WHERE archived_at IS NULL;
