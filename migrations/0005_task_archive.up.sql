-- Tidying up the backlog: terminal tasks (done/failed/cancelled) can be
-- archived instead of filling the board forever. Archived = hidden, but fully
-- preserved (history, recording references stay valid).
ALTER TABLE backlog_tasks ADD COLUMN archived_at timestamptz;

CREATE INDEX idx_backlog_tasks_active ON backlog_tasks (agent_id) WHERE archived_at IS NULL;
