-- When a member's own row last changed (#447): read position, mute, joining,
-- leaving. A client that refreshes its list of conversations asks "what
-- changed since" — a new message moves conversations.last_message_at, and
-- everything that is the member's own moves this.
ALTER TABLE conversation_members ADD COLUMN changed_at timestamptz NOT NULL DEFAULT now();
