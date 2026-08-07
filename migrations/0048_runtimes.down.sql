-- Zurueck auf den Pool unter dem Secret-Schluessel. Die Politik-Spalten kommen
-- wieder, die Werte darin gehen verloren: welche Runtime welchen Deckel gesetzt
-- hatte, laesst sich auf Schluesselebene nicht abbilden, sobald zwei Runtimes
-- denselben Schluessel benutzen. Das ist der Preis des Rueckwegs.
ALTER TABLE secrets
    ADD COLUMN label             TEXT NOT NULL DEFAULT '',
    ADD COLUMN cooldown_until    TIMESTAMPTZ,
    ADD COLUMN cooldown_reason   TEXT NOT NULL DEFAULT '',
    ADD COLUMN limit_amount      NUMERIC(14,4) NOT NULL DEFAULT 0,
    ADD COLUMN limit_unit        TEXT          NOT NULL DEFAULT 'usd',
    ADD COLUMN limit_window_secs INTEGER       NOT NULL DEFAULT 0;
ALTER TABLE secrets ADD CONSTRAINT secrets_limit_unit_chk
    CHECK (limit_unit IN ('usd', 'tokens'));

UPDATE secrets s SET label = rc.label,
       cooldown_until = rc.cooldown_until, cooldown_reason = rc.cooldown_reason,
       limit_amount = rc.limit_amount, limit_unit = rc.limit_unit,
       limit_window_secs = rc.limit_window_secs
FROM runtime_credentials rc JOIN runtimes r ON r.id = rc.runtime_id
WHERE s.org_id = r.org_id AND s.key = rc.secret_key AND s.slot = rc.secret_slot
  AND s.agent_id IS NULL;

CREATE TABLE secret_bindings (
    org_id    UUID     NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    key       TEXT     NOT NULL,
    agent_id  UUID     NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    slot      SMALLINT NOT NULL,
    home_slot SMALLINT,
    reason    TEXT     NOT NULL DEFAULT 'initial',
    bound_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, key, agent_id)
);
INSERT INTO secret_bindings (org_id, key, agent_id, slot, home_slot, reason, bound_at)
SELECT r.org_id, rc.secret_key, b.agent_id, rc.secret_slot, home.secret_slot, b.reason, b.bound_at
FROM runtime_bindings b
JOIN runtimes r ON r.id = b.runtime_id
JOIN runtime_credentials rc ON rc.runtime_id = b.runtime_id AND rc.ord = b.ord
LEFT JOIN runtime_credentials home ON home.runtime_id = b.runtime_id AND home.ord = b.home_ord
ON CONFLICT DO NOTHING;

DROP INDEX idx_cost_runtime_cred;
ALTER TABLE cost_entries DROP COLUMN runtime_id, DROP COLUMN credential_ord;
ALTER TABLE agents DROP COLUMN runtime_id;
DROP TABLE runtime_bindings;
DROP TABLE runtime_credentials;
DROP INDEX idx_runtimes_org;
DROP TABLE runtimes;
