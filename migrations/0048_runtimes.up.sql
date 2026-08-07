-- Die Runtime als Vertrag: Engine plus die Kapazität, sie zu betreiben.
--
-- Bisher stand in agents.runtime ein Framework-Name ("claude-code"), und das
-- Credential wurde ueber eine hartkodierte Namenskonvention gesucht
-- (anthropic_api_key, dann claude_code_oauth_token). Das traegt genau so lange,
-- wie es einen Anbieter und ein Konto gibt.
--
-- Sobald eine Organisation mehrere Vertraege haelt — drei Abo-Sitze und einen
-- API-Key, spaeter dazu ChatGPT —, ist "welche Runtime" keine technische
-- Eigenschaft mehr, sondern eine kaufmaennische: auf wessen Vertrag arbeitet
-- dieser Mitarbeiter. Das ist eine Entscheidung, die ein Mensch trifft und die
-- im Org-Chart beantwortbar sein muss. Details in spec/18-runtimes-capacity.md.

-- WAS fuer ein Arbeitsplatz.
--
-- org_id NOT NULL: eine Runtime traegt Credentials, und ein Credential ueber
-- Mandanten hinweg waere ein Kanal zwischen ihnen — dieselbe Begruendung, mit
-- der in spec/16 ein Runner genau eine Covey-Instanz bedient. Der Speicher hatte
-- das ohnehin entschieden: die AES-GCM-AAD bindet jeden Ciphertext an seine
-- Organisation (D13).
CREATE TABLE runtimes (
    id           UUID PRIMARY KEY,
    org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    engine       TEXT NOT NULL,
    display_name TEXT NOT NULL,
    -- Das Modell gehoert dem Vertrag, nicht dem Agenten: ein Abo-Sitz kann sich
    -- das grosse Modell leisten, wo ein metered Key es nicht kann. Leer = die
    -- Voreinstellung der Engine.
    model        TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_runtimes_org ON runtimes (org_id, display_name);

-- WOMIT er arbeitet. ord IST die Merit Order.
--
-- Die beiden Kapazitaetsarten ziehen gegeneinander: ein Abo ist bezahlt, unge-
-- nutztes Kontingent ist verbranntes Geld, man will es voll fahren. Ein API-Key
-- kostet pro Token, man will ihn leer lassen. Gleichverteilung ueber beide
-- liefert das Schlechteste aus beidem. Richtig ist eine Merit Order wie beim
-- Kraftwerkseinsatz — und sie ist hier schlicht die Reihenfolge, die jemand
-- aufgeschrieben hat, keine versteckte Heuristik.
--
-- kind ist der Name aus der Credential-Deklaration der Engine (api_key,
-- subscription). Daraus weiss die Engine, ob der Wert als ENV-Variable oder als
-- Datei ankommt und ob metered oder quota gilt.
CREATE TABLE runtime_credentials (
    runtime_id        UUID     NOT NULL REFERENCES runtimes(id) ON DELETE CASCADE,
    ord               SMALLINT NOT NULL,
    kind              TEXT     NOT NULL,
    -- Zeiger in den Secret-Store. Der Wert selbst bleibt dort: dort liegen
    -- Verschluesselung, AAD und die Sensibel-Regel, und die ein zweites Mal zu
    -- haben waere der teuerste Weg zu demselben Ergebnis.
    secret_key        TEXT     NOT NULL,
    secret_slot       SMALLINT NOT NULL DEFAULT 0,
    label             TEXT     NOT NULL DEFAULT '',
    -- Zustand: NULL = gesund und waehlbar. Gesetzt wird es aus zwei Richtungen —
    -- vom weichen Limit unten und vom harten Signal (die API hat den Wert
    -- tatsaechlich abgewiesen).
    cooldown_until    TIMESTAMPTZ,
    cooldown_reason   TEXT     NOT NULL DEFAULT '',
    -- Der Deckel in einem rollierenden Fenster. Seit die Engine ihre Auslastung
    -- melden kann, raet das Feld nicht mehr die Decke des Anbieters, sondern
    -- deckelt sie politisch ("diese Gruppe darf hoechstens 60 % des Sitzes").
    -- limit_window_secs = 0 heisst: kein Limit.
    limit_amount      NUMERIC(14,4) NOT NULL DEFAULT 0,
    limit_unit        TEXT          NOT NULL DEFAULT 'usd',
    limit_window_secs INTEGER       NOT NULL DEFAULT 0,
    PRIMARY KEY (runtime_id, ord),
    CONSTRAINT runtime_credentials_unit_chk CHECK (limit_unit IN ('usd', 'tokens'))
);

-- WER sitzt auf welchem Credential.
--
-- Bewusst gespeichert und nicht gerechnet (etwa hash(agent_id) % anzahl): beim
-- Hinzufuegen eines weiteren Tokens wuerfelt eine Modulo-Auswahl ALLE Agenten
-- neu durch, und weil die Engine den Prompt-Praefix pro Credential cached, waere
-- auf einen Schlag jeder Cache kalt — als Nebenwirkung einer Ergaenzung, die
-- niemandem wehtun sollte.
--
-- home_ord ist der Stammplatz: gesetzt, solange der Agent ausgewichen ist. Damit
-- ist die Rueckkehr gezielt moeglich, sobald der Stammplatz wieder gesund ist,
-- statt bei jeder Auswahl neu zu verteilen.
CREATE TABLE runtime_bindings (
    runtime_id UUID     NOT NULL REFERENCES runtimes(id) ON DELETE CASCADE,
    agent_id   UUID     NOT NULL REFERENCES agents(id)   ON DELETE CASCADE,
    ord        SMALLINT NOT NULL,
    home_ord   SMALLINT,
    reason     TEXT     NOT NULL DEFAULT 'initial',
    bound_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (runtime_id, agent_id)
);

ALTER TABLE agents ADD COLUMN runtime_id UUID REFERENCES runtimes(id) ON DELETE SET NULL;

-- Die Kostenzuordnung wandert eine Ebene hoch: nicht mehr auf (Schluessel,
-- Slot), sondern auf (Runtime, Credential). Der Altbestand behaelt seine alten
-- Spalten und bleibt hier NULL — aus welchem Vertrag ein vergangener Lauf
-- bezahlt wurde, laesst sich nicht rekonstruieren, und eine erfundene Zuordnung
-- waere schlimmer als eine fehlende.
ALTER TABLE cost_entries
    ADD COLUMN runtime_id     UUID,
    ADD COLUMN credential_ord SMALLINT;
CREATE INDEX idx_cost_runtime_cred
    ON cost_entries (runtime_id, credential_ord, created_at)
    WHERE runtime_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Bestandsdaten ueberfuehren.
--
-- Fuer jede Organisation und jede Engine, die dort tatsaechlich benutzt wird,
-- entsteht eine Runtime. Ihr Name ist der Engine-Name; wer mehrere Vertraege
-- trennen will, legt danach weitere an und weist um.
INSERT INTO runtimes (id, org_id, engine, display_name)
SELECT gen_random_uuid(), a.org_id, a.runtime, a.runtime
FROM agents a
GROUP BY a.org_id, a.runtime;

UPDATE agents a SET runtime_id = r.id
FROM runtimes r WHERE r.org_id = a.org_id AND r.engine = a.runtime;

-- Die bekannten LLM-Schluessel werden zu Credentials der passenden Runtime, in
-- der Reihenfolge der bisherigen Vorrangregel (API-Key vor Abo-Token). Cooldown,
-- Limit und Label wandern unveraendert mit.
INSERT INTO runtime_credentials
    (runtime_id, ord, kind, secret_key, secret_slot, label,
     cooldown_until, cooldown_reason, limit_amount, limit_unit, limit_window_secs)
SELECT r.id,
       (ROW_NUMBER() OVER (PARTITION BY r.id
            ORDER BY CASE s.key WHEN 'anthropic_api_key' THEN 0 ELSE 1 END, s.slot) - 1)::SMALLINT,
       CASE s.key WHEN 'anthropic_api_key' THEN 'api_key' ELSE 'subscription' END,
       s.key, s.slot, s.label,
       s.cooldown_until, s.cooldown_reason, s.limit_amount, s.limit_unit, s.limit_window_secs
FROM secrets s
JOIN runtimes r ON r.org_id = s.org_id AND r.engine = 'claude-code'
WHERE s.agent_id IS NULL AND s.key IN ('anthropic_api_key', 'claude_code_oauth_token');

-- Die Sitzbelegung wandert mit, damit niemand seinen Platz durch die Migration
-- verliert und alle Prompt-Caches auf einmal kalt werden.
INSERT INTO runtime_bindings (runtime_id, agent_id, ord, home_ord, reason, bound_at)
SELECT rc.runtime_id, b.agent_id, rc.ord, home.ord, b.reason, b.bound_at
FROM secret_bindings b
JOIN agents a ON a.id = b.agent_id AND a.org_id = b.org_id
JOIN runtime_credentials rc
     ON rc.runtime_id = a.runtime_id AND rc.secret_key = b.key AND rc.secret_slot = b.slot
LEFT JOIN runtime_credentials home
     ON home.runtime_id = a.runtime_id AND home.secret_key = b.key AND home.secret_slot = b.home_slot
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- Und was aus secrets wird: Speicher, sonst nichts.
--
-- Von allem, was der Pool an die Tabelle gehaengt hat, bleibt genau eine Spalte
-- — slot. Dass ein Schluessel mehrere Werte tragen kann, ist eine
-- Speicheraussage und gehoert neben Verschluesselung, AAD und Sensibel-Regel.
-- Die Auswahl darunter (Klebrigkeit, Cooldown, Limits) ist Kapazitaetspolitik
-- und wohnt jetzt oben. Man sah der alten Platzierung die zu tiefe Ebene an: die
-- Auswahl bekam eine Verbrauchsfunktion gereicht, weil der Store die Daten fuer
-- seine eigene Entscheidung nicht hatte, und ihr Cooldown wurde von einem
-- LLM-API-Fehler ausgeloest, von dem ein Secret-Store nichts wissen sollte.
DROP TABLE secret_bindings;
ALTER TABLE secrets DROP CONSTRAINT secrets_limit_unit_chk;
ALTER TABLE secrets
    DROP COLUMN label,
    DROP COLUMN cooldown_until,
    DROP COLUMN cooldown_reason,
    DROP COLUMN limit_amount,
    DROP COLUMN limit_unit,
    DROP COLUMN limit_window_secs;
