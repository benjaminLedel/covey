ALTER TABLE organizations DROP COLUMN conversations_since;
ALTER TABLE backlog_tasks DROP COLUMN conversation_id;
DROP INDEX idx_task_transitions_blocked;
DROP TABLE conversation_messages;
DROP TABLE conversation_members;
DROP TABLE conversations;
