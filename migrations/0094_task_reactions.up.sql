-- Reaktionen auf einen Vorgang.
--
-- Sie hängen an der AUFGABE und nicht an einem einzelnen Eintrag des
-- Verlaufs, und das ist keine Vereinfachung: Die Einträge eines Verlaufs sind
-- abgeleitet — eine Nachricht IST die Aufgabe, eine Frage ist ein Übergang,
-- ein Ergebnis ist eine Spalte. Nur die Aufgabe hat eine Kennung, die
-- morgen noch dieselbe ist.
--
-- Der Verfasser ist derselbe Text wie bei einer Notiz ('agent',
-- 'human:<mail>'), damit „wer hat reagiert" überall dieselbe Form hat. Das
-- eindeutige Tripel sorgt dafür, dass zweimal dasselbe Zeichen von derselben
-- Person eine Reaktion bleibt und nicht zwei werden.
CREATE TABLE task_reactions (
    id         UUID PRIMARY KEY,
    task_id    UUID NOT NULL REFERENCES backlog_tasks(id) ON DELETE CASCADE,
    emoji      TEXT NOT NULL,
    author     TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (task_id, emoji, author)
);

-- Gelesen wird ausschließlich je Aufgabe, und der Verlauf holt zwanzig auf
-- einmal.
CREATE INDEX idx_task_reactions_task ON task_reactions(task_id);
