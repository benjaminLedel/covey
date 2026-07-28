-- Binär-Artefakte des Recordings (v. a. Browser-Screenshots) liegen out-of-band
-- in einer eigenen Tabelle, referenziert per id im Event-Payload — nicht inline
-- im JSONB, das würde die Recording-Timeline aufblähen (spec/06).
CREATE TABLE recording_blobs (
    id         uuid PRIMARY KEY,
    org_id     uuid NOT NULL,
    agent_id   uuid NOT NULL,
    task_id    uuid,
    mime       text NOT NULL,
    bytes      bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Für Pruning nach Alter (Bild-Blobs werden gröber geprunt als Text-Events).
CREATE INDEX recording_blobs_created_idx ON recording_blobs (created_at);
