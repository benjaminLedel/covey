-- Request log: the HTTP requests at the edges of the platform (spec/06).
--
-- The recording (recording_events) captures WHAT an agent did — which
-- action with which params and whether it succeeded. What stood on the wire
-- is not in there: the bot connector call to Teams, the reply of the target
-- system, the incoming webhook that failed the signature check. That is
-- exactly what you need when hooking up a target system.
--
-- Hence its own flat table instead of yet another event kind: its own
-- retention window (requests are diagnostic data, not an audit trail), its
-- own indexes and no bloat of the agent timeline.
--
-- org_id/agent_id/task_id are nullable: an incoming webhook is logged even
-- when it is rejected before an agent is resolved — that very case is the
-- interesting one.
CREATE TABLE request_log (
    id          bigserial PRIMARY KEY,
    created_at  timestamptz NOT NULL DEFAULT now(),
    org_id      uuid,
    agent_id    uuid,
    task_id     uuid,
    -- direction: 'in'  = Covey received the request (webhook, trigger)
    --            'out' = Covey made the request (target-system API)
    direction   text   NOT NULL,
    -- system: target-system name of the plugin ('teams', 'zammad', …); empty
    -- when the request cannot be assigned to a plugin.
    system      text   NOT NULL DEFAULT '',
    method      text   NOT NULL DEFAULT '',
    url         text   NOT NULL DEFAULT '',
    status      int    NOT NULL DEFAULT 0,
    duration_ms bigint NOT NULL DEFAULT 0,
    req_bytes   bigint NOT NULL DEFAULT 0,
    resp_bytes  bigint NOT NULL DEFAULT 0,
    -- Bodies are truncated (first ~8 KiB) and redacted (tokens, passwords).
    req_body    text   NOT NULL DEFAULT '',
    resp_body   text   NOT NULL DEFAULT '',
    error       text   NOT NULL DEFAULT '',
    remote      text   NOT NULL DEFAULT ''
);

-- The list always reads "newest first", optionally filtered by
-- system/direction; the pruning runs on the age.
CREATE INDEX request_log_id_desc_idx ON request_log (id DESC);
CREATE INDEX request_log_system_idx ON request_log (system, id DESC);
CREATE INDEX request_log_created_idx ON request_log (created_at);
