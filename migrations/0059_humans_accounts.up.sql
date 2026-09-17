-- P1: the login moves from the seat to the account.
--
-- Up to here humans was both — membership AND login. From now on accounts
-- carries the login, humans stays the seat in an organisation. The
-- way there is a backfill, and it is unambiguous, because humans.email was
-- globally UNIQUE until today: exactly one account per person, nothing to merge.
--
-- humans.email and humans.password_hash stay for one release.
-- During a rolling deploy an old binary would otherwise run that still logs in
-- there, against a database where the columns are already gone.
--
-- feature-requests/002-plattform-registrierung.md

-- 1. Two cases in which the backfill would NOT be unambiguous. Both abort
--    the migration instead of guessing: which person gets which login is a
--    decision of the operator, not of a migration.
DO $$
DECLARE treffer TEXT;
BEGIN
    -- a) Two seats whose addresses differ only in casing. In humans
    --    'Max@x.de' and 'max@x.de' are two allowed rows, in
    --    accounts they would be ONE account — and so one login for two people.
    SELECT lower(email) INTO treffer
    FROM humans GROUP BY lower(email) HAVING count(*) > 1 LIMIT 1;
    IF treffer IS NOT NULL THEN
        RAISE EXCEPTION 'Migration gestoppt: % existiert mehrfach in humans (verschiedene Schreibweisen). Diese Zeilen zu einem Konto zu verschmelzen hiesse, einem Menschen den Zugang eines anderen zu geben — bitte die Adressen vorher vereindeutigen.', treffer;
    END IF;

    -- b) A self-registered account carries the address of an existing
    --    seat. Someone from outside would then have chosen the password for
    --    this membership. Registration prevents this since this
    --    migration (internal/accounts); for accounts that came into
    --    being before it, the operator decides.
    SELECT a.email INTO treffer
    FROM accounts a JOIN humans h ON lower(h.email) = a.email LIMIT 1;
    IF treffer IS NOT NULL THEN
        RAISE EXCEPTION 'Migration gestoppt: fuer % gibt es ein selbstregistriertes Konto UND einen bestehenden Sitz. Bitte das Konto pruefen und loeschen, bevor die Anmeldung darauf umgestellt wird.', treffer;
    END IF;
END $$;

-- 2. The connection.
ALTER TABLE humans ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE CASCADE;

-- 3. One account per person. The password hash moves over verbatim (Argon2id):
--    nobody has to set their password again. The address counts as confirmed —
--    it comes from an administrator, not from a self-registration.
INSERT INTO accounts (id, email, password_hash, display_name, email_verified_at)
SELECT gen_random_uuid(), lower(h.email), h.password_hash, h.display_name, now()
FROM humans h;

UPDATE humans h SET account_id = a.id FROM accounts a WHERE a.email = lower(h.email);

ALTER TABLE humans ALTER COLUMN account_id SET NOT NULL;

-- 4. The rule that excluded multiple memberships up to now falls away.
--    Two tighter ones take its place: one address once per organisation,
--    and an account sits in an organisation only once.
ALTER TABLE humans DROP CONSTRAINT humans_email_key;
CREATE UNIQUE INDEX humans_email_per_org   ON humans (org_id, lower(email));
CREATE UNIQUE INDEX humans_account_per_org ON humans (org_id, account_id);

-- 5. The session hangs on the account; the seat in it is the ACTIVE membership.
--    human_id NULL means: logged in, but no organisation yet — a state that
--    could not exist before, and one that self-registration
--    needs.
ALTER TABLE http_sessions ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE CASCADE;
UPDATE http_sessions s SET account_id = h.account_id FROM humans h WHERE h.id = s.human_id;
ALTER TABLE http_sessions ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE http_sessions ALTER COLUMN human_id DROP NOT NULL;
