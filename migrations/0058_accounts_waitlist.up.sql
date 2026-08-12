-- Das Konto ueber der Mitgliedschaft, und die Codes, mit denen es entsteht.
--
-- Bis hierher war Anmeldung gleich Mitgliedschaft: eine Zeile in humans traegt
-- E-Mail, Passwort, Organisation und Rolle zugleich. Das schliesst aus, dass
-- eine Person in zwei Organisationen arbeitet — und es schliesst aus, dass eine
-- Person ueberhaupt existiert, bevor ihre Organisation es tut. Genau das
-- braucht die Selbstregistrierung: erst das Konto, dann Beitritt oder
-- Gruendung.
--
-- humans bleibt deshalb, was es ist — der Sitz in einer Organisation, auf den
-- zehn Fremdschluessel zeigen. Darueber liegt accounts, die Anmeldung. Verknuepft
-- werden beide, wenn die Sitzung auf das Konto umgestellt wird (P1 in FR-002);
-- bis dahin steht accounts fuer sich, und die Registrierung fuellt es.
--
-- feature-requests/002-plattform-registrierung.md
CREATE TABLE accounts (
    id                UUID PRIMARY KEY,
    email             TEXT NOT NULL UNIQUE, -- immer kleingeschrieben abgelegt
    password_hash     TEXT NOT NULL,
    display_name      TEXT NOT NULL DEFAULT '',
    -- NULL = noch nicht bestaetigt. Solange kein Mailversand eingerichtet ist,
    -- setzt die Registrierung den Zeitpunkt sofort: eine Bestaetigung, die
    -- niemand verschicken kann, waere ein Konto, das niemand je benutzt.
    email_verified_at TIMESTAMPTZ,
    -- Die Instanz-Ebene, ausdruecklich keine Organisations-Rolle: platform_admin
    -- vergibt jede Organisation an sich selbst, system_admin niemand.
    platform_role     TEXT NOT NULL DEFAULT 'user'
                      CHECK (platform_role IN ('user','system_admin')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at     TIMESTAMPTZ
);

-- Der Wartelisten-Code. Abgelegt wird nur sein Hash, wie bei den Sitzungen:
-- wer die Datenbank liest, bekommt daraus keine gueltigen Codes. Der Klartext
-- existiert genau einmal, im Moment der Erzeugung.
--
-- Was am Code haengt, steht nicht im Code, sondern hier: wie oft er gilt, bis
-- wann, und ob er in eine bestimmte Organisation fuehrt (dann tritt sein
-- Inhaber bei, statt zu gruenden) oder nur fuer eine E-Mail-Domain gilt.
CREATE TABLE waitlist_codes (
    code_hash     TEXT PRIMARY KEY,
    label         TEXT NOT NULL DEFAULT '',   -- "Konferenz X", "Pilotkunde Y"
    max_uses      INTEGER NOT NULL DEFAULT 1,
    used_count    INTEGER NOT NULL DEFAULT 0,
    expires_at    TIMESTAMPTZ,
    org_id        UUID REFERENCES organizations(id) ON DELETE CASCADE,
    email_pattern TEXT NOT NULL DEFAULT '',   -- z. B. "@firma.de"
    created_by    UUID REFERENCES humans(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at    TIMESTAMPTZ,
    CHECK (max_uses > 0)
);

-- Wer welchen Code eingeloest hat. Der Primaerschluessel verhindert nebenbei,
-- dass dasselbe Konto denselben Code zweimal verbraucht.
CREATE TABLE waitlist_redemptions (
    code_hash   TEXT NOT NULL REFERENCES waitlist_codes(code_hash) ON DELETE CASCADE,
    account_id  UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    redeemed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (code_hash, account_id)
);
