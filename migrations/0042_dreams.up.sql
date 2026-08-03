-- Träume (spec/05): Der Agent räumt sein Gedächtnis nachts auf — er verschmilzt
-- Dubletten, benennt Tagebuch-Titel in Entitäts-Titel um, und künftig mehr
-- (verlinken, einordnen, Verwahrlostes wegräumen). Bisher lief dieser Pass
-- flüchtig: die Dubletten-Konsolidierung im 10-Minuten-Ticker hinterließ nur
-- eine Zeile im wiki_log, der Titel-Pass gar nichts Dauerhaftes.
--
-- Das reicht nicht, sobald der Lauf nachts unbeaufsichtigt schreibt. Wer
-- morgens sehen will, was in der Nacht mit dem Gedächtnis seines Agenten
-- passiert ist, braucht zwei Dinge: den Traum als Ganzes (wann, wie lange, ob
-- er durchlief) und jede einzelne Änderung mit ihrem Vorher — sonst ist
-- „rückgängig" nicht mehr als ein Wort.
--
-- Bewusst getrennt vom wiki_log: das protokolliert einzelne Schreibvorgänge am
-- Wiki, unabhängig davon, wer sie ausgelöst hat. Ein Traum ist die Klammer
-- darüber und trägt Absicht, Kosten und Zusammenhang.
CREATE TABLE dreams (
    id          UUID PRIMARY KEY,
    agent_id    UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    -- manual | nightly — woher der Traum kam. Steht in der UI, weil es den
    -- Unterschied macht, ob jemand zugesehen hat.
    trigger     TEXT NOT NULL DEFAULT 'manual',
    -- running | done | error. Ein beim Neustart hängengebliebener Traum bleibt
    -- auf running stehen; das ist ehrlicher als ihn stillschweigend zu
    -- vollenden, und der Start-Endpunkt räumt ihn nach einer Frist ab.
    status      TEXT NOT NULL DEFAULT 'running',
    error       TEXT NOT NULL DEFAULT '',
    -- Phase des laufenden Traums (merge | titles | …) für die Fortschrittsanzeige.
    phase       TEXT NOT NULL DEFAULT '',
    -- Wie viele Seiten der Traum angesehen hat, und wie viele er nicht mehr
    -- geschafft hat (Deckel je Durchlauf) — beides gehört ins Protokoll, damit
    -- ein Traum nicht vollständiger aussieht, als er war.
    looked_at   INT NOT NULL DEFAULT 0,
    skipped     INT NOT NULL DEFAULT 0,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ
);
CREATE INDEX idx_dreams_agent ON dreams (agent_id, started_at DESC);

-- Eine einzelne Handlung im Traum. `before` trägt den Zustand davor und ist
-- damit die Grundlage für das Rückgängigmachen; `undone_at` hält fest, dass
-- jemand von diesem Recht Gebrauch gemacht hat, statt die Zeile zu löschen —
-- ein zurückgenommener Traumschritt ist selbst eine Information.
CREATE TABLE dream_actions (
    id         UUID PRIMARY KEY,
    dream_id   UUID NOT NULL REFERENCES dreams(id) ON DELETE CASCADE,
    -- retitle | merge (später: link, classify, prune)
    kind       TEXT NOT NULL,
    page_slug  TEXT NOT NULL DEFAULT '',
    before     TEXT NOT NULL DEFAULT '',
    after      TEXT NOT NULL DEFAULT '',
    -- Begründung des Modells, in seinen Worten. Ohne sie ist eine Umbenennung
    -- eine Behauptung, die niemand nachvollziehen kann.
    reason     TEXT NOT NULL DEFAULT '',
    undone_at  TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_dream_actions_dream ON dream_actions (dream_id, created_at);
