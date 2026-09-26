-- A note's properties (#373): a status, a date and tags, as a Notion
-- database has them — the notes can then be seen as a table or a board.
ALTER TABLE human_notes
    ADD COLUMN status text NOT NULL DEFAULT '' CHECK (status IN ('', 'todo', 'doing', 'done')),
    ADD COLUMN due date,
    ADD COLUMN tags text[] NOT NULL DEFAULT '{}';
