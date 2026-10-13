-- Files carried by a chat message (#586), in both directions: what an agent
-- hands a person through covey/message, and what a person attaches for an
-- agent. The bytes live here and not in the recording's blob table: a
-- recording has a depth and a retention of its own, and an attachment is part
-- of what was said — it goes when the message goes, and not before.
CREATE TABLE conversation_attachments (
    id              uuid PRIMARY KEY,
    message_id      uuid NOT NULL REFERENCES conversation_messages(id) ON DELETE CASCADE,
    -- Denormalised like the message's own: the download checks the reader
    -- against the conversation without a join through the message.
    conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            text NOT NULL,
    mime            text NOT NULL,
    size            bigint NOT NULL,
    -- Where the file was put in the home of the agents a person's message
    -- addressed, relative to the home ('' for an agent's own file).
    home_path       text NOT NULL DEFAULT '',
    bytes           bytea NOT NULL,
    -- The order the files were attached in, which is the order they are shown.
    position        int NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_conversation_attachments_message ON conversation_attachments(message_id);
