-- Der Vorschlag: eine gespeicherte Config-Version, die NICHT in Kraft ist.
--
-- agent_config_versions nummeriert pro Agent und behandelt die höchste Nummer
-- als die Wahrheit — es gibt dort keine Version, die daliegt und nicht läuft.
-- Genau die braucht der Betriebsingenieur (spec/21): er liest die Arbeitsakte
-- eines Kollegen und schlägt eine Änderung vor, wirksam wird sie erst, wenn
-- ein Mensch sie annimmt. Deshalb steht ein Vorschlag in einer eigenen Tabelle
-- und nicht mit einem Flag in der Versionsfolge: was dort steht, läuft.
--
-- Bewusst eine Tabelle für DREI Ergebnisse und nicht nur für den Vorschlag.
-- Ein Review endet in einer von drei Diagnosen (spec/21): die Config ist
-- falsch -> ein Vorschlag mit Diff; der Auftrag ist falsch -> ein Befund an den
-- Menschen, der ihn verantwortet, ohne Diff; die Plattform ist falsch -> ein
-- Issue, das schon im Tracker liegt. Alle drei brauchen dieselbe Person und
-- denselben Posteingang. Die Plattform kennt keine Nachricht an einen
-- Menschen, und eine für dieses Feature zu erfinden hieße, einen zweiten,
-- schlechteren Posteingang neben den zu stellen, den es ohnehin geben muss.
CREATE TABLE improvement_items (
    id            UUID PRIMARY KEY,
    org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    -- Der Kollege, um den es geht. Nicht der Absender: der steht in
    -- author_agent_id, und die beiden dürfen nie derselbe sein (spec/21,
    -- „er begutachtet sich nicht selbst").
    agent_id      UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN ('proposal','finding','issue')),
    title         TEXT NOT NULL,
    -- Die Begründung: warum, aus welcher Beobachtung. Das ist der Text, den
    -- ein Mensch liest, bevor er den Diff liest.
    rationale     TEXT NOT NULL DEFAULT '',

    -- Nur beim Vorschlag: die inaktive Version.
    -- base_version ist die Version, GEGEN die geschrieben wurde. Sie macht den
    -- Vorschlag zu einem Diff mit Basis — wird der Agent zwischenzeitlich von
    -- Hand geändert, überschreibt eine Annahme diese Änderung nicht still,
    -- sondern zeigt den Konflikt an. Derselbe Fall wie bei einem Pull Request,
    -- und dieselbe Antwort. 0 = kein Vorschlag.
    base_version  INTEGER NOT NULL DEFAULT 0,
    -- Nur die Dateien, die der Vorschlag ÄNDERT — nicht der ganze Satz.
    -- Angenommen wird gemergt, nie ersetzt: was hier fehlt, bleibt stehen.
    files         JSONB NOT NULL DEFAULT '{}'::jsonb,

    -- Herkunft, von der Plattform geschrieben und nicht vom Modell gemeldet.
    -- NULL beim author = ein Mensch hat den Punkt angelegt.
    author_agent_id UUID REFERENCES agents(id) ON DELETE SET NULL,
    task_id         UUID REFERENCES backlog_tasks(id) ON DELETE SET NULL,

    -- Die Entscheidung. Ein abgelehnter Vorschlag bleibt stehen, mitsamt dem
    -- Grund: er ist das Nützlichste, was jemand lesen kann, der den
    -- Betriebsingenieur selbst überprüfen will.
    status        TEXT NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending','accepted','rejected')),
    decided_by    UUID REFERENCES humans(id) ON DELETE SET NULL,
    decided_at    TIMESTAMPTZ,
    decision_note TEXT NOT NULL DEFAULT '',
    -- Die Version, die aus der Annahme hervorging (0 = keine). Sie entsteht auf
    -- dem normalen Schreibweg, mit dem Menschen als created_by.
    applied_version INTEGER NOT NULL DEFAULT 0,

    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Die Abfrage der Liste: offene Punkte einer Organisation, neueste zuerst.
CREATE INDEX idx_improvement_org_status ON improvement_items (org_id, status, created_at DESC);
-- Und die zweite Ansicht: was liegt zu diesem Kollegen an (Mitarbeiter-Profil).
CREATE INDEX idx_improvement_agent ON improvement_items (agent_id, created_at DESC);
