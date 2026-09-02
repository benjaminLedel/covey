-- A stored value has a life beyond being stored: the target system stops
-- accepting it one day, and until now the first sign was a 401 inside a
-- recording, filed under the action that hit it and read as a permission
-- problem weeks later (#176). These columns describe that life — they are
-- storage about the value, not a policy about choosing between values (that
-- distinction is why 0048 moved cooldowns and limits off this table, and it
-- still holds).
--
-- expires_at    when the system will stop accepting it: entered by hand, or
--               reported by the plugin where the system states it
-- rejected_at   the system refused the credential itself (a 401), with reason
-- probed_at     the last connection test, its outcome and the identity seen
-- credential_id the system's own id for the value (a PAT id) — what a rotation
--               revokes afterwards
-- rotatable     the plugin can mint this value's successor
-- warned_at     a person has been told about the current state; cleared with it
ALTER TABLE secrets
    ADD COLUMN expires_at      TIMESTAMPTZ,
    ADD COLUMN rejected_at     TIMESTAMPTZ,
    ADD COLUMN rejected_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN probed_at       TIMESTAMPTZ,
    ADD COLUMN probe_error     TEXT NOT NULL DEFAULT '',
    ADD COLUMN probe_identity  TEXT NOT NULL DEFAULT '',
    ADD COLUMN credential_id   TEXT NOT NULL DEFAULT '',
    ADD COLUMN rotatable       BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN warned_at       TIMESTAMPTZ;
