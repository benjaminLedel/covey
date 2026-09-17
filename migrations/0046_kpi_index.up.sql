-- The index the KPIs run on (spec/17-kpis.md).
--
-- A KPI is a counting rule over the action events: "how often
-- zammad:reply_external, in period X, for agent Y". The existing indexes on
-- recording_events do not carry that — they are built on (agent_id, id) and
-- (task_id, id), so on paging through a recording, not on aggregating over
-- a period.
--
-- Partial on kind='action': the action events are the smaller part of the
-- table (every run writes many runtime events and few actions), and no KPI
-- ever asks after anything else. The index therefore stays small enough to
-- sit in cache.
--
-- The expression payload->>'action' sits deliberately IN the index and not in
-- a generated column: ADD COLUMN ... GENERATED rewrites the whole table and
-- locks it while doing so. recording_events only grows (there is no
-- retention, the whole KPI history rests on that) — on a running instance
-- that would be a maintenance window for a column nobody reads.
--
-- No CONCURRENTLY: the migrator runs each migration in a transaction, and
-- there it is not allowed. Defensible because the migrations run when
-- `covey serve` starts — at that moment no agent is working that would have
-- to wait for writing an event.
CREATE INDEX idx_recording_kpi
    ON recording_events (agent_id, (payload->>'action'), created_at)
    WHERE kind = 'action';
