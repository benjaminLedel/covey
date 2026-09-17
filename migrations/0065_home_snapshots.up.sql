-- The home store: the home as a whole, not as a list.
--
-- Until now the persistent home was a directory beside the control plane, and
-- with that an agent's binding to its machine was a precondition and
-- not an optimisation. If the directory is lost, the work is gone — of
-- a measured developer home (7.1 GB) 48 MB exist nowhere else,
-- and they lie scattered over the whole home.
--
-- That is why the whole home moves into a central, content-addressed store
-- after every job and is materialised from it on wake.
-- The question "which of this is valuable?" is never asked. Details in
-- spec/16-runner.md, "The central home store".

CREATE TABLE home_snapshots (
    id            UUID PRIMARY KEY,
    org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id      UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    -- Where the working copy lay when the snapshot was taken. ON DELETE SET NULL:
    -- a decommissioned runner takes its working copy with it, not the snapshot.
    runner_id     UUID REFERENCES runners(id) ON DELETE SET NULL,
    -- The snapshot IS this hash: the manifest itself lies as a block in the
    -- store, and everything else here is for display.
    manifest_hash TEXT NOT NULL,
    total_size    BIGINT NOT NULL DEFAULT 0,
    -- What actually moved up. The difference to total_size is the
    -- real statement: a 7-GB home, but maybe 200 MB that only
    -- this agent holds.
    blocks_up     INTEGER NOT NULL DEFAULT 0,
    bytes_up      BIGINT NOT NULL DEFAULT 0,
    reason        TEXT NOT NULL DEFAULT 'job',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_home_snapshots_agent ON home_snapshots (agent_id, created_at DESC);
CREATE INDEX idx_home_snapshots_org ON home_snapshots (org_id, created_at DESC);

-- The scheduler's preference: there the working copy lies warm. Explicitly
-- a hint and not a promise — nothing in the system may assume that a
-- home is still there.
ALTER TABLE agents ADD COLUMN last_runner_id UUID REFERENCES runners(id) ON DELETE SET NULL;
