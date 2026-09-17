-- Audit trail for admin actions taken by PEOPLE.
--
-- What agents do stands in the recording (recording_events) — complete, since
-- the MVP. What people do to the platform stood nowhere: filing secrets,
-- deleting guard rails, changing roles, pulling the emergency stop, lowering
-- the recording level. The last two are exactly the moves somebody would make
-- before an overstep.
--
-- Deliberately its own table instead of recording_events: there every row hangs
-- on an agent (agent_id NOT NULL), while admin actions often have none — a role
-- change or a new secret concerns the organisation.
--
-- Deliberately WITHOUT request bodies: they would hold secret values and
-- passwords. What is kept is WHO TOUCHED WHAT WHEN (method, path, outcome) —
-- not the content. The path carries the ids, which keeps "who deleted guard
-- rail X" answerable.
CREATE TABLE audit_log (
    id          BIGSERIAL PRIMARY KEY,
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    -- Who acted. NULL when the account is deleted later — the action stays on
    -- regardless, with the e-mail as an aid to memory.
    actor_id    UUID REFERENCES humans(id) ON DELETE SET NULL,
    actor_email TEXT NOT NULL DEFAULT '',
    actor_role  TEXT NOT NULL DEFAULT '',
    method      TEXT NOT NULL,
    path        TEXT NOT NULL,
    status      INTEGER NOT NULL,
    -- The client IP, as far as it can be told (behind a proxy, that proxy's address).
    client_ip   TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The query the view uses: newest entries of one organisation.
CREATE INDEX idx_audit_org_time ON audit_log (org_id, id DESC);
