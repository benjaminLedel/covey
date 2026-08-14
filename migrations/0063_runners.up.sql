-- Der Runner: der Ort, an dem eine Sandbox tatsaechlich laeuft.
--
-- Bisher startete die Control Plane Sandboxen direkt ueber die lokale
-- Docker-CLI, und der Egress-Proxy las seine Allowlist selbst aus Postgres.
-- Beides traegt genau so lange, wie Control Plane und Rechenlast auf derselben
-- Maschine sitzen. Spaetestens auf einem fremden Host hiesse es, die
-- Postgres-Zugangsdaten an jede Maschine zu verteilen — der Proxy ist aber ein
-- Durchsetzungspunkt, kein Datenbank-Client. Details in spec/16-runner.md.
--
-- Diese Migration legt nur die Identitaet an, gegen die sich der Proxy
-- authentifiziert. Das Protokoll, die Zuteilung und der ferne Runner kommen
-- spaeter (Stufen 2 und 4 der Baureihenfolge in spec/16).

-- org_id NOT NULL: ein Runner haelt Homes und Daemon-Tokens, und beides ist
-- Eigentum genau eines Mandanten. Geteilter Blockspeicher zwischen zwei
-- Organisationen waere ein Kanal zwischen ihnen — dieselbe Begruendung, mit der
-- runtimes.org_id NOT NULL ist (0048).
CREATE TABLE runners (
    id           UUID PRIMARY KEY,
    org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    -- builtin: laeuft im covey-serve-Prozess, wird von der Plattform selbst
    -- angelegt und tritt ab, sobald die Organisation einen eigenen
    -- registriert. remote: per Registrierungs-Token hinzugekommen.
    kind         TEXT NOT NULL CHECK (kind IN ('builtin', 'remote')),
    -- Leer beim eingebauten: sein Name ist ein UI-Text und gehoert in die
    -- Uebersetzungsdateien, nicht in die Datenbank.
    name         TEXT NOT NULL DEFAULT '',
    -- Nur der Hash. Beim eingebauten Runner wird das Token bei jedem Start neu
    -- gewuerfelt und liegt ausschliesslich im Prozess — ein langlebiges
    -- Geheimnis waere hier nichts wert, weil es niemand ausserhalb braucht.
    token_hash   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ
);

-- Hoechstens ein eingebauter je Organisation; registrierte duerfen viele sein.
CREATE UNIQUE INDEX idx_runners_builtin ON runners (org_id) WHERE kind = 'builtin';
CREATE INDEX idx_runners_org ON runners (org_id, kind);

-- Bestandsorganisationen bekommen ihren eingebauten Runner sofort, damit der
-- Proxy nach dem Upgrade eine Identitaet hat. Der Token-Hash bleibt leer und
-- wird beim naechsten Start gesetzt.
INSERT INTO runners (id, org_id, kind)
SELECT gen_random_uuid(), o.id, 'builtin' FROM organizations o;
