-- Lead of a department: one or more members that the department
-- is under. A lead is either a human OR an agent; a member
-- can lead several departments without belonging to them.
CREATE TABLE department_leads (
    department_id UUID NOT NULL REFERENCES departments(id) ON DELETE CASCADE,
    human_id      UUID REFERENCES humans(id) ON DELETE CASCADE,
    agent_id      UUID REFERENCES agents(id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((human_id IS NULL) <> (agent_id IS NULL))
);

CREATE UNIQUE INDEX department_leads_human_uq ON department_leads (department_id, human_id) WHERE human_id IS NOT NULL;
CREATE UNIQUE INDEX department_leads_agent_uq ON department_leads (department_id, agent_id) WHERE agent_id IS NOT NULL;
