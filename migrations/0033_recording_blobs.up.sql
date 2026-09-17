-- Binary artefacts of the recording (mainly browser screenshots) live
-- out-of-band in their own table, referenced by id in the event payload — not
-- inline in the JSONB, that would bloat the recording timeline (spec/06).
CREATE TABLE recording_blobs (
    id         uuid PRIMARY KEY,
    org_id     uuid NOT NULL,
    agent_id   uuid NOT NULL,
    task_id    uuid,
    mime       text NOT NULL,
    bytes      bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- For pruning by age (image blobs are pruned coarser than text events).
CREATE INDEX recording_blobs_created_idx ON recording_blobs (created_at);
