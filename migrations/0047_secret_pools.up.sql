-- Ein Secret-Schlüssel darf mehrere Werte tragen (ein "Pool").
--
-- Anlass sind die Claude-Code-Tokens: wer mehrere Abo-Sitze hat, will sie auf
-- die Agenten verteilen, statt alle Agenten durch ein Token zu schicken und im
-- Limit zu landen. Dasselbe gilt für Bot-Konten bei GitLab/GitHub — deshalb
-- sitzt der Pool unter JEDEM Schlüssel und nicht in einer Sonderbehandlung für
-- Anthropic.
--
-- slot nummeriert die Werte eines Schlüssels. Alle Bestandszeilen werden Slot 0,
-- damit bleibt das heutige Verhalten Wort für Wort erhalten: ein Schlüssel, ein
-- Wert. Die Zuweisung an Agenten (secret_assignments) bleibt bewusst auf
-- SCHLÜSSEL-Ebene — welcher Slot es dann wird, entscheidet die Auswahl, nicht
-- die Verwaltung. Sonst müsste man bei jedem neuen Token alle Zuweisungen
-- nachziehen.
ALTER TABLE secrets
    ADD COLUMN slot  SMALLINT NOT NULL DEFAULT 0,
    ADD COLUMN label TEXT     NOT NULL DEFAULT '';

-- Der Zustand eines einzelnen Werts. cooldown_until IS NULL heißt: gesund und
-- wählbar. Gesetzt wird es aus zwei Richtungen — vom weichen Limit unten
-- (Verbrauch im Fenster erschöpft) und vom harten Signal (die API hat das Token
-- tatsächlich abgewiesen: 429, abgelaufen, widerrufen).
ALTER TABLE secrets
    ADD COLUMN cooldown_until  TIMESTAMPTZ,
    ADD COLUMN cooldown_reason TEXT NOT NULL DEFAULT '';

-- Das konfigurierte Limit eines Werts, in einem rollierenden Fenster.
--
-- Zwei Einheiten, weil "Verbrauch" je nach Credential etwas anderes ist: bei
-- einem API-Key ist es Geld (usd, die Zahl aus cost_entries ist dort echte
-- Abrechnung), bei einem Abo-Token ist Geld eine Fiktion — man zahlt es nicht,
-- das echte Limit ist Anthropics rollierendes Fenster. Dort zählen Tokens als
-- Näherung. Die Näherung STEUERT nur; die Wahrheit bleibt das harte Signal.
--
-- limit_window_secs = 0 heißt: kein Limit. Das ist der Default und damit der
-- Zustand jedes Bestandswerts.
ALTER TABLE secrets
    ADD COLUMN limit_amount      NUMERIC(14,4) NOT NULL DEFAULT 0,
    ADD COLUMN limit_unit        TEXT          NOT NULL DEFAULT 'usd',
    ADD COLUMN limit_window_secs INTEGER       NOT NULL DEFAULT 0;

ALTER TABLE secrets ADD CONSTRAINT secrets_limit_unit_chk
    CHECK (limit_unit IN ('usd', 'tokens'));

-- Eindeutig ist ein Secret jetzt erst mit dem Slot. Die beiden partiellen
-- Indizes aus 0007 werden dafür neu gezogen; ihre Rolle (org-weit vs.
-- agent-eigen) bleibt unverändert.
DROP INDEX uq_secrets_org;
DROP INDEX uq_secrets_agent;
CREATE UNIQUE INDEX uq_secrets_org   ON secrets (org_id, key, slot) WHERE agent_id IS NULL;
CREATE UNIQUE INDEX uq_secrets_agent ON secrets (org_id, agent_id, key, slot) WHERE agent_id IS NOT NULL;

-- Wer sitzt auf welchem Wert.
--
-- Bewusst eine gespeicherte Bindung statt einer gerechneten (etwa
-- hash(agent_id) % anzahl): Beim Hinzufügen eines weiteren Tokens würfelt eine
-- Modulo-Auswahl ALLE Agenten neu durch. Jeder Agent bekäme ein anderes
-- Credential, und weil Claude Code den Prompt-Präfix pro Credential cached,
-- wäre auf einen Schlag jeder Cache kalt — als Nebenwirkung einer Ergänzung,
-- die niemandem wehtun sollte. Eine gespeicherte Bindung überlebt Änderungen am
-- Pool, ein neuer Wert wird nur von neuen Agenten und von Ausweichern belegt.
--
-- home_slot ist der Stammplatz: gesetzt, solange der Agent ausgewichen ist.
-- Damit ist die Rückkehr gezielt möglich, sobald der Stammplatz wieder gesund
-- ist, statt bei jeder Auswertung neu zu verteilen.
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

-- Die Kostenzuordnung auf den Wert, auf dem der Lauf ging.
--
-- Ohne diese beiden Spalten ist kein Limit pro Token messbar, keine Auslastung
-- anzeigbar und die Frage "lohnt sich ein weiterer Sitz" nicht beantwortbar:
-- cost_entries kannte bisher nur Agent, Aufgabe und Modell.
--
-- Nullable, und der Altbestand bleibt NULL — aus welchem Token ein vergangener
-- Lauf bezahlt wurde, lässt sich nicht rekonstruieren. Die Auswertung muss
-- damit umgehen, dass alte Zeilen keine Zuordnung tragen; sie zählen weiter in
-- die Gesamtkosten, nur nicht in die Aufschlüsselung pro Wert.
ALTER TABLE cost_entries
    ADD COLUMN secret_key  TEXT,
    ADD COLUMN secret_slot SMALLINT;

-- Der Index, auf dem die Fensterabfrage läuft. Sie läuft bei JEDER Auswahl
-- (einmal pro Lauf, für jeden Wert mit konfiguriertem Limit), muss also billig
-- sein. Partiell, weil der Altbestand und jeder Lauf ohne Zuordnung nichts
-- beiträgt.
CREATE INDEX idx_cost_secret_slot
    ON cost_entries (secret_key, secret_slot, created_at)
    WHERE secret_key IS NOT NULL;
