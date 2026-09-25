-- The builtin media store (#344, spec/14): media in a person's notes.
--
-- The bytes live here because an installation's notes are small enough for
-- the database it already has; mediastore.Store is the port an S3-compatible
-- store replaces this table behind. A medium belongs to a seat (owner_id) and
-- is read only by it; there is no listing — a note's reference finds it.
CREATE TABLE media (
    id           uuid PRIMARY KEY,
    org_id       uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    owner_id     uuid NOT NULL REFERENCES humans(id) ON DELETE CASCADE,
    content_type text NOT NULL,
    size         integer NOT NULL CHECK (size >= 0),
    data         bytea NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_media_owner ON media (owner_id);
