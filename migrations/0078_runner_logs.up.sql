-- What a runner says, where somebody can read it.
--
-- Until now it said it to its own stderr — journald on the host, reachable by
-- SSH and by nobody else. That is the wrong place for the one component whose
-- whole point is that it stands on a machine the control plane does not own:
-- the question "why did that host stop taking sandboxes at three in the
-- morning" was answerable only by whoever has a shell on it, and only for as
-- long as the journal kept it.
--
-- In Postgres and not in a ring buffer in memory, because the interesting case
-- is precisely the one a ring buffer loses: the runner that went away. A log
-- that is gone after the restart answers every question except the one that
-- was asked.
CREATE TABLE runner_logs (
    id         bigserial PRIMARY KEY,
    runner_id  uuid NOT NULL REFERENCES runners(id) ON DELETE CASCADE,
    org_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    ts         timestamptz NOT NULL,
    level      text NOT NULL,
    msg        text NOT NULL,
    attrs      jsonb,
    -- The agent a line belongs to, where it belongs to one: a start writes
    -- about a particular agent, a reconnect about nobody.
    agent_id   uuid
);

-- The one query there is: this runner, newest first, optionally from a level
-- upwards. The index carries it whole.
CREATE INDEX runner_logs_runner_idx ON runner_logs (runner_id, ts DESC);

-- The level a runner is asked to report at. It sits on the row and not only in
-- the message, so that a runner which reconnects comes back at the level the
-- interface shows — otherwise somebody switches a host to debug, it drops out
-- for a minute, and comes back quietly at info while the switch still reads
-- "debug".
ALTER TABLE runners ADD COLUMN log_level text NOT NULL DEFAULT 'info';
