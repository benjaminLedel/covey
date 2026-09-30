-- Configuration changes proposed from the chat (#491), a trial behind an
-- organisation setting.
--
-- A person who writes "check your queue only on weekdays" to an agent gets a
-- drafted configuration change instead of a task. The draft is the same kind
-- of row covey/propose_agent_config writes (0053): a stored config version
-- that is not in effect until a human accepts it. What is new is where it
-- came from — not a task of an agent, but a message of a person — so the row
-- carries that message and that person.
--
-- Off by default: an installation that upgrades changes nothing.
ALTER TABLE organizations ADD COLUMN chat_config_proposals BOOLEAN NOT NULL DEFAULT false;

-- The message the proposal was drafted from, and the person who wrote it.
-- SET NULL on both: a deleted message or seat leaves the proposal and its
-- decision standing.
ALTER TABLE improvement_items
    ADD COLUMN origin_message_id UUID REFERENCES conversation_messages(id) ON DELETE SET NULL,
    ADD COLUMN requested_by      UUID REFERENCES humans(id) ON DELETE SET NULL;
