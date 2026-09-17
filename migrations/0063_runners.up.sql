-- The runner: the place where a sandbox actually runs.
--
-- Until now the control plane started sandboxes directly via the local
-- Docker CLI, and the egress proxy read its allowlist from Postgres itself.
-- Both hold exactly as long as the control plane and the compute load sit on
-- the same machine. On a foreign host it would at the latest mean handing the
-- Postgres credentials to every machine — but the proxy is an enforcement
-- point, not a database client. Details in spec/16-runner.md.
--
-- This migration only creates the identity against which the proxy
-- authenticates. The protocol, the assignment and the remote runner come
-- later (stages 2 and 4 of the build order in spec/16).

-- org_id NOT NULL: a runner holds homes and daemon tokens, and both are the
-- property of exactly one tenant. Shared block storage between two
-- organisations would be a channel between them — the same reason
-- runtimes.org_id is NOT NULL (0048).
CREATE TABLE runners (
    id           UUID PRIMARY KEY,
    org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    -- builtin: runs in the covey-serve process, is created by the platform
    -- itself and steps down as soon as the organisation registers one of
    -- its own. remote: added by a registration token.
    kind         TEXT NOT NULL CHECK (kind IN ('builtin', 'remote')),
    -- Empty for the builtin one: its name is a UI text and belongs in the
    -- translation files, not in the database.
    name         TEXT NOT NULL DEFAULT '',
    -- Only the hash. For the builtin runner the token is redrawn at every
    -- start and lives exclusively in the process — a long-lived secret would
    -- be worth nothing here, because nobody outside needs it.
    token_hash   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ
);

-- At most one builtin per organisation; registered ones may be many.
CREATE UNIQUE INDEX idx_runners_builtin ON runners (org_id) WHERE kind = 'builtin';
CREATE INDEX idx_runners_org ON runners (org_id, kind);

-- Existing organisations get their builtin runner right away, so that the
-- proxy has an identity after the upgrade. The token hash stays empty and
-- is set at the next start.
INSERT INTO runners (id, org_id, kind)
SELECT gen_random_uuid(), o.id, 'builtin' FROM organizations o;
