-- What a workflow's trigger remembers between firings (#566): when its
-- schedule last fired and the gate's work signature then — the same two
-- things agent_heartbeats keeps for a heartbeat — and the hash of its token,
-- if the token trigger is on. The token itself is shown once and kept
-- nowhere; a definition never carries it.
CREATE TABLE workflow_triggers (
    workflow_id    UUID PRIMARY KEY REFERENCES workflows(id) ON DELETE CASCADE,
    last_fired_at  TIMESTAMPTZ,
    last_work_sig  TEXT NOT NULL DEFAULT '',
    token_hash     TEXT UNIQUE
);
