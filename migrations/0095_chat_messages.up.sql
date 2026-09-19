-- Nachrichten als eigenes Objekt — und der Schalter, der darüber entscheidet.
--
-- Bis hierher war jede Nachricht eine Aufgabe, und der Verlauf war eine
-- Ansicht auf den Backlog. Das trug, solange jede Nachricht Arbeit war. Es
-- trägt nicht mehr, sobald ein Agent auch antworten darf: Eine Antwort auf
-- „ist das gestern rausgegangen?" hat keine Aufgabe, an der sie hängen
-- könnte, und eine Aufgabe dafür anzulegen wäre genau das, was die Triage
-- vermeiden soll (#302).
--
-- Also: Die Nachricht ist der Umschlag, der Backlog bleibt das Hauptbuch.
-- `task_id` ist gesetzt, wenn aus der Nachricht Arbeit geworden ist, und
-- leer, wenn der Agent geantwortet hat. Beides steht im selben Verlauf.
CREATE TABLE chat_messages (
    id         UUID PRIMARY KEY,
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id   UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    -- Dieselbe Form wie bei einer Notiz: 'chat:<mail>' oder 'agent'.
    author     TEXT NOT NULL,
    text       TEXT NOT NULL,
    -- Woraus Arbeit wurde, zeigt hierhin. ON DELETE SET NULL: Wird eine
    -- Aufgabe gelöscht, bleibt die Nachricht — sie ist das Gesagte, nicht
    -- das Getane.
    task_id    UUID REFERENCES backlog_tasks(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Gelesen wird ausschließlich als Verlauf eines Agenten, neueste zuerst.
CREATE INDEX idx_chat_messages_agent ON chat_messages(agent_id, created_at DESC);
-- Und einmal rückwärts: „hat diese Aufgabe eine Nachricht?" fragt der Verlauf
-- für jede Aufgabe seines Fensters.
CREATE INDEX idx_chat_messages_task ON chat_messages(task_id) WHERE task_id IS NOT NULL;

-- Der Schalter je Organisation. 'off' ist die Vorgabe und bedeutet genau das
-- bisherige Verhalten: Jede Nachricht wird eine Aufgabe. Eine Installation,
-- die aktualisiert, ändert damit nichts, bis jemand es einschaltet.
ALTER TABLE organizations ADD COLUMN chat_triage TEXT NOT NULL DEFAULT 'off';
