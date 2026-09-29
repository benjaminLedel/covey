-- Conversations between people and agents (#440).
--
-- Until here the chat was one thread per agent (chat_messages, 0095), shared
-- by everybody in the organisation, and it mirrored every task of the agent.
-- A conversation is now an object with members: direct (exactly two — a
-- person and an agent, or two people) or a group. It sits beside the backlog
-- and is not a view on it; a task points at the conversation it reports to
-- (backlog_tasks.conversation_id), and nothing else of the backlog enters.
--
-- chat_messages and chat_reads stay as they are, for the audit. No row of
-- them is carried over: every conversation starts empty.
CREATE TABLE conversations (
    id               uuid PRIMARY KEY,
    org_id           uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    kind             text NOT NULL CHECK (kind IN ('direct', 'group')),
    -- Groups only; a direct conversation is named after the other member.
    title            text,
    -- The two members of a direct conversation, sorted
    -- ('agent:<id>|human:<id>'). UNIQUE is what keeps it one per pair, also
    -- when two requests open it at the same moment.
    direct_key       text UNIQUE,
    created_by_human uuid,
    created_at       timestamptz NOT NULL DEFAULT now(),
    -- Denormalised from the newest message, so that "my conversations,
    -- newest first" sorts without touching the messages.
    last_message_at  timestamptz NOT NULL DEFAULT now(),
    archived_at      timestamptz,
    CHECK ((kind = 'direct') = (direct_key IS NOT NULL))
);
CREATE INDEX idx_conversations_org ON conversations(org_id, last_message_at DESC);

-- Members point at a human or an agent. No foreign key, as with
-- agents.supervisor_id (0025): the id is one of two tables. A person who
-- leaves keeps the row with left_at set, so the audit still says who was in.
CREATE TABLE conversation_members (
    conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    member_kind     text NOT NULL CHECK (member_kind IN ('human', 'agent')),
    member_id       uuid NOT NULL,
    role            text NOT NULL DEFAULT 'member' CHECK (role IN ('owner', 'member')),
    joined_at       timestamptz NOT NULL DEFAULT now(),
    left_at         timestamptz,
    -- How far the member has read. NULL: nothing yet, counted from joined_at.
    last_read_at    timestamptz,
    muted           boolean NOT NULL DEFAULT false,
    PRIMARY KEY (conversation_id, member_kind, member_id)
);
-- The one query a surface asks on every look: which conversations am I in.
CREATE INDEX idx_conversation_members_member ON conversation_members(member_kind, member_id, conversation_id)
    WHERE left_at IS NULL;

CREATE TABLE conversation_messages (
    id              uuid PRIMARY KEY,
    conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    -- Denormalised for the audit, which reads across conversations.
    org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    -- 'system' is the platform speaking about itself; the reports of a task
    -- are written in the agent's name and carry 'agent'.
    author_kind     text NOT NULL CHECK (author_kind IN ('human', 'agent', 'system')),
    author_id       uuid,
    text            text NOT NULL,
    -- 'text' for what somebody said; 'result', 'error' and 'question' for
    -- what a task reports back. Room for the cards of #441.
    kind            text NOT NULL DEFAULT 'text',
    -- The work this message opened or reports on. SET NULL: a deleted task
    -- leaves what was said.
    task_id         uuid REFERENCES backlog_tasks(id) ON DELETE SET NULL,
    -- The message this one answers — in a group, a reply to an agent's line
    -- addresses that agent.
    reply_to        uuid,
    meta            jsonb,
    -- '' | pending | done | failed, as on chat_messages (0096).
    triage_state    text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now()
);
-- A page of messages, newest first, with the id as the tie-break.
CREATE INDEX idx_conversation_messages_page ON conversation_messages(conversation_id, created_at DESC, id DESC);
CREATE INDEX idx_conversation_messages_task ON conversation_messages(task_id) WHERE task_id IS NOT NULL;
-- The push round reads a window of time across all conversations.
CREATE INDEX idx_conversation_messages_created ON conversation_messages(created_at);
CREATE INDEX idx_conversation_messages_pending ON conversation_messages(created_at)
    WHERE triage_state = 'pending';

-- Where a task reports back. Set when a conversation opens it, inherited by
-- its continuations; NULL for everything else, which reports nowhere.
ALTER TABLE backlog_tasks ADD COLUMN conversation_id uuid REFERENCES conversations(id) ON DELETE SET NULL;
CREATE INDEX idx_backlog_tasks_conversation ON backlog_tasks(conversation_id) WHERE conversation_id IS NOT NULL;

-- The round that delivers a parked task's question reads the blocked edges of
-- a window of time; without this it would read the whole life of every task.
CREATE INDEX idx_task_transitions_blocked ON task_transitions(created_at) WHERE to_state = 'blocked';

-- When conversations began for an organisation. A question a task parked on
-- before that is not delivered afterwards: the conversations start empty.
-- now() as a column default is taken once for the rows that exist and at
-- insert for every new organisation.
ALTER TABLE organizations ADD COLUMN conversations_since timestamptz NOT NULL DEFAULT now();
