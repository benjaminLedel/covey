DROP INDEX IF EXISTS idx_chat_messages_pending;
ALTER TABLE chat_messages DROP COLUMN triage_state;
