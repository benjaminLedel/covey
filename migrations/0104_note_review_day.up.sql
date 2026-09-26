-- One daily review per day (#368): a note written from the activity log
-- (#363) records its day, and writing the review of that day again replaces
-- it instead of adding another note.
ALTER TABLE human_notes ADD COLUMN review_day date;
CREATE UNIQUE INDEX idx_human_notes_review_day ON human_notes (human_id, review_day) WHERE review_day IS NOT NULL;
