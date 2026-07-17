-- Heartbeats: wiederkehrende Aufgaben aus HEARTBEAT.md, materialisiert je
-- Agent (analog system_accesses aus ACCESS.md). Die Control Plane legt fällige
-- Einträge als Backlog-Aufgabe (origin='heartbeat') an. Genau eine der beiden
-- Zeitplan-Formen ist gesetzt: every_seconds (alle:) oder daily_at (täglich:,
-- Serverzeit). last_fired_at startet bei now(): ein frisch gespeicherter
-- Heartbeat feuert erst nach Ablauf seines Intervalls, nicht sofort.
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
