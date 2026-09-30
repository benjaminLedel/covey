-- A task that is a chat message nobody triaged (#483): without the triage a
-- message to an agent becomes a task, and what that task has to produce is a
-- reply in the conversation, not a report for the record. The flag is on the
-- task because a group message addressed to two agents opens two tasks from
-- one message, and a continuation carries it on.
ALTER TABLE backlog_tasks ADD COLUMN chat_answer boolean NOT NULL DEFAULT false;
