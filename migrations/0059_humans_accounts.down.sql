-- Rolling back works only as long as no account without a seat exists: a
-- self-registered account has no counterpart in humans and would be lost.
-- The rollback therefore aborts instead of deleting silently.
DO $$
DECLARE ohne_sitz INT;
BEGIN
    SELECT count(*) INTO ohne_sitz FROM accounts a
    WHERE NOT EXISTS (SELECT 1 FROM humans h WHERE h.account_id = a.id);
    IF ohne_sitz > 0 THEN
        RAISE EXCEPTION 'Rueckweg gestoppt: % Konten haben keinen Sitz und wuerden verschwinden.', ohne_sitz;
    END IF;
END $$;

-- The passwords back onto the seat, otherwise nobody logs in again after
-- the teardown.
UPDATE humans h SET password_hash = a.password_hash FROM accounts a WHERE a.id = h.account_id;

ALTER TABLE http_sessions DROP COLUMN IF EXISTS account_id;
DELETE FROM http_sessions WHERE human_id IS NULL;
ALTER TABLE http_sessions ALTER COLUMN human_id SET NOT NULL;

DROP INDEX IF EXISTS humans_account_per_org;
DROP INDEX IF EXISTS humans_email_per_org;
ALTER TABLE humans ADD CONSTRAINT humans_email_key UNIQUE (email);
ALTER TABLE humans DROP COLUMN IF EXISTS account_id;
