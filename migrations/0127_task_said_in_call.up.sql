-- A task opened from a message said aloud in a call (#502): its reply or its
-- result is heard as well as read, so it carries a spoken form beside the
-- written one. On the task, written with the row, because the run of a chat
-- answer is told so at dispatch — a flag set a moment after the insert could
-- arrive after the dispatcher has compiled the prompt. A continuation carries
-- it on.
ALTER TABLE backlog_tasks ADD COLUMN said_in_call boolean NOT NULL DEFAULT false;
