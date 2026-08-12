-- P1: die Anmeldung wandert vom Sitz ans Konto.
--
-- Bis hierher war humans beides — Mitgliedschaft UND Login. Ab jetzt traegt
-- accounts die Anmeldung, humans bleibt der Sitz in einer Organisation. Der
-- Weg dahin ist ein Backfill, und er ist eindeutig, weil humans.email bis
-- heute global UNIQUE war: je Mensch genau ein Konto, nichts zusammenzufuehren.
--
-- humans.email und humans.password_hash bleiben eine Release lang stehen.
-- Waehrend eines rollenden Deploys laeuft sonst ein altes Binary, das dort
-- noch anmeldet, gegen eine Datenbank, in der die Spalten schon fehlen.
--
-- feature-requests/002-plattform-registrierung.md

-- 1. Zwei Faelle, bei denen der Backfill NICHT eindeutig waere. Beide brechen
--    die Migration ab, statt zu raten: welcher Mensch welches Login bekommt,
--    ist eine Entscheidung des Betreibers, keine einer Migration.
DO $$
DECLARE treffer TEXT;
BEGIN
    -- a) Zwei Sitze, deren Adressen sich nur in der Schreibweise unterscheiden.
    --    In humans sind 'Max@x.de' und 'max@x.de' zwei erlaubte Zeilen, in
    --    accounts waeren sie EIN Konto — und damit ein Login fuer zwei Menschen.
    SELECT lower(email) INTO treffer
    FROM humans GROUP BY lower(email) HAVING count(*) > 1 LIMIT 1;
    IF treffer IS NOT NULL THEN
        RAISE EXCEPTION 'Migration gestoppt: % existiert mehrfach in humans (verschiedene Schreibweisen). Diese Zeilen zu einem Konto zu verschmelzen hiesse, einem Menschen den Zugang eines anderen zu geben — bitte die Adressen vorher vereindeutigen.', treffer;
    END IF;

    -- b) Ein selbstregistriertes Konto traegt die Adresse eines bestehenden
    --    Sitzes. Dann haette jemand von aussen das Passwort zu dieser
    --    Mitgliedschaft gewaehlt. Seit dieser Migration verhindert die
    --    Registrierung das (internal/accounts); fuer Konten, die vorher
    --    entstanden sind, entscheidet der Betreiber.
    SELECT a.email INTO treffer
    FROM accounts a JOIN humans h ON lower(h.email) = a.email LIMIT 1;
    IF treffer IS NOT NULL THEN
        RAISE EXCEPTION 'Migration gestoppt: fuer % gibt es ein selbstregistriertes Konto UND einen bestehenden Sitz. Bitte das Konto pruefen und loeschen, bevor die Anmeldung darauf umgestellt wird.', treffer;
    END IF;
END $$;

-- 2. Die Verbindung.
ALTER TABLE humans ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE CASCADE;

-- 3. Je Mensch ein Konto. Der Passwort-Hash wandert woertlich mit (Argon2id):
--    niemand muss sein Passwort neu setzen. Die Adresse gilt als bestaetigt —
--    sie stammt von einem Administrator, nicht aus einer Selbstregistrierung.
INSERT INTO accounts (id, email, password_hash, display_name, email_verified_at)
SELECT gen_random_uuid(), lower(h.email), h.password_hash, h.display_name, now()
FROM humans h;

UPDATE humans h SET account_id = a.id FROM accounts a WHERE a.email = lower(h.email);

ALTER TABLE humans ALTER COLUMN account_id SET NOT NULL;

-- 4. Die Regel, die Mehrfach-Mitgliedschaft bisher ausgeschlossen hat, faellt.
--    An ihre Stelle treten zwei engere: eine Adresse einmal je Organisation,
--    und ein Konto sitzt in einer Organisation nur einmal.
ALTER TABLE humans DROP CONSTRAINT humans_email_key;
CREATE UNIQUE INDEX humans_email_per_org   ON humans (org_id, lower(email));
CREATE UNIQUE INDEX humans_account_per_org ON humans (org_id, account_id);

-- 5. Die Sitzung haengt am Konto; der Sitz darin ist die AKTIVE Mitgliedschaft.
--    human_id NULL heisst: angemeldet, aber noch keine Organisation — der
--    Zustand, den es bisher nicht geben konnte und den die Selbstregistrierung
--    braucht.
ALTER TABLE http_sessions ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE CASCADE;
UPDATE http_sessions s SET account_id = h.account_id FROM humans h WHERE h.id = s.human_id;
ALTER TABLE http_sessions ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE http_sessions ALTER COLUMN human_id DROP NOT NULL;
