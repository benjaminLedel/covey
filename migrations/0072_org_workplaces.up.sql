-- Own workplaces: the image that an organisation brings itself.
--
-- Until now it stood as free text on the agent (agents.sandbox_image). That cost
-- three things, and all three go away with this table:
--
--  1. It was invisible. An image that only stands in one field on an agent
--     shows up in no overview — anyone who wanted to know which
--     own images this organisation uses at all had to open every
--     agent on its own.
--  2. It was undescribed. A registry address does not say what is inside it.
--     The next agent then had the same address there again, and
--     whether the two meant the same thing, only knew who had set them.
--  3. It was one typo away from the failure: misspelled
--     it only surfaces at the wake, in the record of a run.
--
-- Now a workplace is a named thing that one creates once and
-- selects afterwards — just like the profiles from the catalogue (spec/16). The
-- agent still carries only a NAME; which image stands behind it
-- the organisation decides at one place.
CREATE TABLE org_workplaces (
    id          UUID PRIMARY KEY,
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    -- The name that an agent carries. Like a profile name from the catalogue, and
    -- therefore in the same namespace: whoever wants `base` as a name would have
    -- to explain which of the two is meant. The collision is caught by the server
    -- (internal/httpapi), not by this table — it does not know the catalogue.
    name        TEXT NOT NULL,
    label       TEXT NOT NULL DEFAULT '',
    -- What this workplace is for. The reason why the table is more
    -- than a list of addresses: it answers the question that a
    -- registry address leaves open.
    description TEXT NOT NULL DEFAULT '',
    -- The image reference, the way docker understands it.
    image       TEXT NOT NULL,
    created_by  UUID REFERENCES humans(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A name belongs to exactly one workplace within an organisation.
CREATE UNIQUE INDEX idx_org_workplaces_name ON org_workplaces (org_id, name);
