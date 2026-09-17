-- Heartbeats: recurring tasks from HEARTBEAT.md, materialized per
-- agent (analogous to system_accesses from ACCESS.md). The control plane creates due
-- entries as a backlog task (origin='heartbeat'). Exactly one of the two
-- schedule forms is set: every_seconds (alle:) or daily_at (täglich:,
-- server time). last_fired_at starts at now(): a freshly stored
-- heartbeat fires only after its interval has passed, not right away.
CREATE TABLE agent_heartbeats (
    agent_id      UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    task_body     TEXT NOT NULL DEFAULT '',
    every_seconds BIGINT,
    daily_at      TIME,
    last_fired_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, name),
    CHECK ((every_seconds IS NULL) <> (daily_at IS NULL)),
    CHECK (every_seconds IS NULL OR every_seconds > 0)
);
