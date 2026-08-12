-- Das Review: was der Betrieb ueber einen Kollegen geschrieben hat, datiert.
--
-- Bewusst NICHT in improvement_items. Ein offener Punkt wartet auf die
-- Entscheidung eines Menschen und verschwindet danach aus dem Vorrat; ein
-- Review wartet auf nichts. Es ist die Akte neben der Akte: die Seite, die
-- jemand oeffnet, wenn er wissen will, was mit einem Agenten los ist — mit
-- einer Geschichte statt einer Zahl ohne eine (spec/21).
--
-- Die Verbindung zu den Punkten, die aus ihm hervorgingen, laeuft ueber die
-- AUFGABE und nicht ueber einen Fremdschluessel: beide entstehen im selben
-- Lauf, tragen dieselbe task_id, und eine zweite Verknuepfung waere eine
-- zweite Wahrheit, die auseinanderlaufen kann.
CREATE TABLE agent_reviews (
    id              UUID PRIMARY KEY,
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    -- Der begutachtete Kollege.
    agent_id        UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    -- Wer es geschrieben hat. NULL, wenn der Agent spaeter geloescht wird —
    -- das Review bleibt trotzdem stehen.
    author_agent_id UUID REFERENCES agents(id) ON DELETE SET NULL,
    task_id         UUID REFERENCES backlog_tasks(id) ON DELETE SET NULL,
    -- Der Zeitraum, ueber den geurteilt wurde. Ohne ihn ist „acht Abbrueche"
    -- keine Aussage.
    period_from     TIMESTAMPTZ NOT NULL,
    period_to       TIMESTAMPTZ NOT NULL,
    -- Der Text. Markdown, wie alles, was ein Mensch hier liest.
    summary         TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Die eine Abfrage: die Historie eines Kollegen, neueste zuerst.
CREATE INDEX idx_agent_reviews_agent ON agent_reviews (agent_id, created_at DESC);

-- Und die Adresse fuer ein Issue, das schon im Tracker liegt. Ein Bericht ohne
-- den Link dorthin zwingt jeden Leser zur Suche.
ALTER TABLE improvement_items ADD COLUMN link TEXT NOT NULL DEFAULT '';
