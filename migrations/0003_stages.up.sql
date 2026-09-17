-- Custom stages per agent: freely definable kanban columns as an overlay over
-- the lifecycle state. The machine state (open/in_progress/blocked/done/…)
-- stays untouched and still carries scheduler, wake and completion; stage is
-- the workflow position an agent or a human moves, purely for display.
CREATE TABLE agent_stages (
    id         UUID PRIMARY KEY,
    agent_id   UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    position   INTEGER NOT NULL DEFAULT 0,
    color      TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (agent_id, name)
);

CREATE INDEX idx_agent_stages_agent ON agent_stages(agent_id, position, created_at);

-- stage_id is nullable: a task without a stage lands in the "Ohne Stage" bucket.
-- When a stage is deleted its tasks fall back to NULL (no loss).
ALTER TABLE backlog_tasks
    ADD COLUMN stage_id UUID REFERENCES agent_stages(id) ON DELETE SET NULL;

CREATE INDEX idx_backlog_tasks_stage ON backlog_tasks(stage_id);
