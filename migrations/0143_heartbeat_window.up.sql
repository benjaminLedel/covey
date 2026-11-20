-- Heartbeats: a time-of-day window (nur-zwischen: HH:MM-HH:MM [Zone], #760).
-- Outside it the entry is not due — it neither fires nor advances
-- last_fired_at, so the first tick inside the window fires it. A window may
-- wrap midnight (start > end). window_zone is an IANA name the window is read
-- in; empty means server time, as for daily_at.
ALTER TABLE agent_heartbeats
    ADD COLUMN window_start TIME,
    ADD COLUMN window_end   TIME,
    ADD COLUMN window_zone  TEXT NOT NULL DEFAULT '',
    ADD CONSTRAINT agent_heartbeats_window_both
        CHECK ((window_start IS NULL) = (window_end IS NULL)),
    ADD CONSTRAINT agent_heartbeats_window_not_empty
        CHECK (window_start IS NULL OR window_start <> window_end);

-- The clock time a window is read in: the zone's local time, or server time
-- when no zone is set. A function rather than an inline CASE, so the scheduler
-- and the status view evaluate the same thing.
CREATE FUNCTION heartbeat_local_time(zone TEXT) RETURNS TIME
LANGUAGE sql STABLE AS $$
    SELECT CASE WHEN zone = '' THEN LOCALTIME
                ELSE (now() AT TIME ZONE zone)::time END
$$;
