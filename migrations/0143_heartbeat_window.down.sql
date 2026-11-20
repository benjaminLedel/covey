DROP FUNCTION heartbeat_local_time(TEXT);
ALTER TABLE agent_heartbeats
    DROP CONSTRAINT agent_heartbeats_window_not_empty,
    DROP CONSTRAINT agent_heartbeats_window_both,
    DROP COLUMN window_zone,
    DROP COLUMN window_end,
    DROP COLUMN window_start;
