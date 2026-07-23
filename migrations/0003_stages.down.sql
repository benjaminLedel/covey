DROP INDEX IF EXISTS idx_backlog_tasks_stage;
ALTER TABLE backlog_tasks DROP COLUMN IF EXISTS stage_id;
DROP TABLE IF EXISTS agent_stages;
