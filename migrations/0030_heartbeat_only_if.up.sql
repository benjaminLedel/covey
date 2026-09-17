-- Heartbeats: optional fire condition nur-wenn: <system>. Before the backlog
-- task is created the control plane asks the target system plugin whether
-- there is any work (e.g. unread mail over IMAP) — if there is none,
-- the run falls away and with it the (expensive) agent wake. Empty = fire always.
ALTER TABLE agent_heartbeats ADD COLUMN only_if TEXT NOT NULL DEFAULT '';
