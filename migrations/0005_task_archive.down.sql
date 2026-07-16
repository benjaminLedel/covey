DROP INDEX IF EXISTS idx_backlog_tasks_active;
ALTER TABLE backlog_tasks DROP COLUMN archived_at;
