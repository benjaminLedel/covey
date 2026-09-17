-- Column origin: stages that the agent itself "invents" via set_stage
-- are marked with created_by='agent' and cleared away automatically
-- as soon as no active (unarchived) task is left in them. Columns created by
-- humans (UI, default stages) stay, even when they are empty.
ALTER TABLE agent_stages
    ADD COLUMN created_by TEXT NOT NULL DEFAULT 'human';

-- Task notes: proactive progress updates by the agent on the task
-- (covey/add_note) — task-related, as opposed to generally valid
-- insights, which go into memory (covey/remember → memories).
CREATE TABLE task_notes (
    id         UUID PRIMARY KEY,
    task_id    UUID NOT NULL REFERENCES backlog_tasks(id) ON DELETE CASCADE,
    author     TEXT NOT NULL DEFAULT 'agent',
    content    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_task_notes_task ON task_notes(task_id, created_at);
