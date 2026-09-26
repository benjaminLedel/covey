-- How far each person has read each agent's thread (#378). The app and the
-- web tell a new answer from an old one by it. Seeded with now() for the
-- seats and agents that exist, so the history before this migration does not
-- all turn up as unread at once.
CREATE TABLE chat_reads (
    human_id uuid NOT NULL REFERENCES humans(id) ON DELETE CASCADE,
    agent_id uuid NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    read_at  timestamptz NOT NULL,
    PRIMARY KEY (human_id, agent_id)
);

INSERT INTO chat_reads (human_id, agent_id, read_at)
SELECT h.id, a.id, now() FROM humans h JOIN agents a ON a.org_id = h.org_id;
