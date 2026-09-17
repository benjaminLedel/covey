-- Task kinship: a task can arise out of another one.
-- Two cases use this:
--
--   * Continuation — a run ended at the turn limit (max_turns) without a result.
--     Instead of letting the next heartbeat start from scratch, a
--     follow-up task comes into being with the handover state as its assignment and
--     the runtime session of the aborted run to resume on.
--   * Subtask — the agent breaks its work down itself (covey/create_task).
--
-- The chain carries the loop guard: via parent_task_id the depth is countable,
-- and a continuation chain breaks off instead of running on endlessly.
-- ON DELETE SET NULL: a deleted origin task orphans its children,
-- does not delete them — the work stays visible.
ALTER TABLE backlog_tasks
    ADD COLUMN parent_task_id UUID REFERENCES backlog_tasks(id) ON DELETE SET NULL;

CREATE INDEX idx_backlog_tasks_parent ON backlog_tasks(parent_task_id);
