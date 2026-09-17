-- Base allowlist per organisation: hosts that EVERY agent of the org may
-- reach — until now hardwired as a code default (api.anthropic.com), now
-- configurable in the UI. Existing orgs are seeded with the previous
-- default so that the migration does not change behavior;
-- new orgs are seeded by org creation.
CREATE TABLE egress_default_hosts (
    id      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id  UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    pattern TEXT NOT NULL,
    note    TEXT NOT NULL DEFAULT '',
    UNIQUE (org_id, pattern)
);

INSERT INTO egress_default_hosts (org_id, pattern, note)
SELECT id, 'api.anthropic.com', 'LLM-Endpunkt der Claude-Runtime'
FROM organizations;
