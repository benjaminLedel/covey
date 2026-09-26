-- Agent suggestions (#370): what a person's activity log (#363) says could be
-- handed to an agent, as computed the last time the person asked. One row
-- per seat, private to it like the log.
CREATE TABLE human_agent_suggestions (
    human_id    uuid PRIMARY KEY REFERENCES humans(id) ON DELETE CASCADE,
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    days        integer NOT NULL,
    sessions    integer NOT NULL,
    suggestions jsonb NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
