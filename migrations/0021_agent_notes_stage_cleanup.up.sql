-- Spalten-Herkunft: Stages, die der Agent selbst per set_stage „erfindet",
-- werden mit created_by='agent' markiert und automatisch wieder abgeräumt,
-- sobald keine aktive (unarchivierte) Aufgabe mehr darin liegt. Menschlich
-- angelegte Spalten (UI, Default-Stages) bleiben stehen, auch wenn sie leer sind.
ALTER TABLE agent_stages
    ADD COLUMN created_by TEXT NOT NULL DEFAULT 'human';

-- Aufgaben-Notizen: proaktive Zwischenstände des Agenten an der Aufgabe
-- (covey/add_note) — aufgabenbezogen, im Gegensatz zu allgemeingültigen
-- Erkenntnissen, die ins Gedächtnis gehen (covey/remember → memories).
CREATE TABLE task_notes (
    id         UUID PRIMARY KEY,
    task_id    UUID NOT NULL REFERENCES backlog_tasks(id) ON DELETE CASCADE,
    author     TEXT NOT NULL DEFAULT 'agent',
    content    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_task_notes_task ON task_notes(task_id, created_at);
