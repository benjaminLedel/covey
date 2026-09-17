-- Heartbeats: signature of the backlog last reported. A nur-wenn:
-- condition is level-driven, not edge-driven — it reports "work is
-- waiting here" for as long as the state holds. An agent that
-- DELIBERATELY ends a run without a comment (say the QA colleague's
-- reply was only an approval) would therefore be woken to the same
-- thing again in the next interval — and in the end comments only to
-- switch off its own alarm clock. With the signature the heartbeat
-- fires again only once the backlog has actually CHANGED (new comment,
-- new MR). Silence thus becomes a valid answer.
ALTER TABLE agent_heartbeats ADD COLUMN last_work_sig TEXT NOT NULL DEFAULT '';
