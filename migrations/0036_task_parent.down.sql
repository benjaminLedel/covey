DROP INDEX IF EXISTS idx_backlog_tasks_parent;

ALTER TABLE backlog_tasks
    DROP COLUMN IF EXISTS parent_task_id;
