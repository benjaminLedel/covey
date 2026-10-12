-- What an agent reported for its task with covey/report (#563): named fields
-- a workflow reads to decide where a case goes next — a merge request, an
-- "accepted" or a list of defects. Kept apart from the result text, which is
-- written for people; a condition needs a value, not a sentence.
ALTER TABLE backlog_tasks ADD COLUMN report jsonb;
