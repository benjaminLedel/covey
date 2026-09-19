-- An index on the column every reader of the history joins on.
--
-- task_transitions carries the whole life of every task and grows monotonically:
-- one row per dispatch, block, completion, retry. Until now the only index on
-- it was the primary key on `id`, which no query uses — ListTransitions and the
-- chat thread both look up by task_id, and both therefore scanned the table.
--
-- That was tolerable while the history was read by somebody who had opened one
-- task. The chat thread joins it on every poll of an open tab, so on an
-- instance that has run for months it became the most expensive query in the
-- application (#298).
CREATE INDEX IF NOT EXISTS idx_task_transitions_task
    ON task_transitions(task_id, created_at);
