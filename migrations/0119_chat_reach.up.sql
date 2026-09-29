-- Whom a member of the organisation may write to directly (#440): 'org' —
-- any agent of the organisation, the default — or 'department' — only the
-- agents of the person's own departments. An agent's supervisor and the org
-- admin reach it either way.
ALTER TABLE organizations ADD COLUMN chat_reach TEXT NOT NULL DEFAULT 'org'
    CHECK (chat_reach IN ('org', 'department'));
