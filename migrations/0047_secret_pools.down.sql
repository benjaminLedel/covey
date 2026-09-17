DROP INDEX idx_cost_secret_slot;
ALTER TABLE cost_entries DROP COLUMN secret_key, DROP COLUMN secret_slot;

DROP TABLE secret_bindings;

-- Back to one value per key: everything beyond slot 0 falls away, otherwise
-- the unique indexes from 0007 could not be rebuilt.
DELETE FROM secrets WHERE slot <> 0;

DROP INDEX uq_secrets_org;
DROP INDEX uq_secrets_agent;
CREATE UNIQUE INDEX uq_secrets_org   ON secrets (org_id, key) WHERE agent_id IS NULL;
CREATE UNIQUE INDEX uq_secrets_agent ON secrets (org_id, agent_id, key) WHERE agent_id IS NOT NULL;

ALTER TABLE secrets DROP CONSTRAINT secrets_limit_unit_chk;
ALTER TABLE secrets
    DROP COLUMN slot,
    DROP COLUMN label,
    DROP COLUMN cooldown_until,
    DROP COLUMN cooldown_reason,
    DROP COLUMN limit_amount,
    DROP COLUMN limit_unit,
    DROP COLUMN limit_window_secs;
