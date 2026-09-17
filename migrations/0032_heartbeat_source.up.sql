-- Heartbeats: separate provenance. 'config' are entries materialised from
-- HEARTBEAT.md (managed by SaveConfig), 'system' are platform defaults the
-- control plane sets for all agents (e.g. the configurable wiki cleanup
-- heartbeat, COVEY_WIKI_CLEANUP). The separation allows reconciling system
-- defaults globally without touching agent-owned heartbeats — and in turn
-- the config sync deletes only 'config' rows. An agent's own HEARTBEAT.md entry
-- of the same name wins (the system default is skipped for this agent).
ALTER TABLE agent_heartbeats ADD COLUMN source TEXT NOT NULL DEFAULT 'config';
