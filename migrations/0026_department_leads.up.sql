-- Leitung einer Abteilung: ein oder mehrere Mitglieder, denen die Abteilung
-- untersteht. Eine Leitung ist entweder ein Mensch ODER ein Agent; ein Mitglied
-- kann mehrere Abteilungen leiten, ohne ihnen anzugehören.
CREATE TABLE department_leads (
    department_id UUID NOT NULL REFERENCES departments(id) ON DELETE CASCADE,
    human_id      UUID REFERENCES humans(id) ON DELETE CASCADE,
    agent_id      UUID REFERENCES agents(id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((human_id IS NULL) <> (agent_id IS NULL))
);

CREATE UNIQUE INDEX department_leads_human_uq ON department_leads (department_id, human_id) WHERE human_id IS NOT NULL;
CREATE UNIQUE INDEX department_leads_agent_uq ON department_leads (department_id, agent_id) WHERE agent_id IS NOT NULL;
