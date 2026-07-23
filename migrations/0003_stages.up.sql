-- Custom-Stages pro Agent: frei definierbare Kanban-Spalten als Overlay über
-- den Lifecycle-State. Der Maschinen-state (open/in_progress/blocked/done/…)
-- bleibt unberührt und trägt weiterhin Scheduler, Wake und Abschluss; stage ist
-- die vom Agenten oder Menschen bewegte, rein anzeigende Workflow-Position.
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

-- stage_id ist nullable: eine Aufgabe ohne Stage landet im „Ohne Stage"-Eimer.
-- Beim Löschen einer Stage fallen ihre Aufgaben auf NULL zurück (kein Verlust).
ALTER TABLE backlog_tasks
    ADD COLUMN stage_id UUID REFERENCES agent_stages(id) ON DELETE SET NULL;

CREATE INDEX idx_backlog_tasks_stage ON backlog_tasks(stage_id);
