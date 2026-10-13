-- Workflows (#561, spec/29): an organisation's definitions, versioned like an
-- agent's configuration, and the runs going through them.
--
-- The definition is kept as the text it was written in. The rules live in
-- internal/workflow and read that text; columns for steps and exits would be
-- a second copy of the format, and the two would drift.
CREATE TABLE workflows (
    id         UUID PRIMARY KEY,
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    slug       TEXT NOT NULL,
    version    INT  NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, slug)
);

CREATE TABLE workflow_versions (
    workflow_id UUID NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    version     INT  NOT NULL,
    source      TEXT NOT NULL,
    created_by  UUID REFERENCES humans(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workflow_id, version)
);

-- One row per case going through a workflow. What the engine keeps beside
-- the position — what each step reported, the rounds a condition counted,
-- who did which step, the log — is JSON: it is read back whole, never
-- queried by part.
CREATE TABLE workflow_runs (
    id          UUID PRIMARY KEY,
    workflow_id UUID NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    case_ref    TEXT NOT NULL,
    seq         BIGINT NOT NULL,
    step        TEXT NOT NULL,
    state       TEXT NOT NULL,
    assignee    TEXT NOT NULL DEFAULT '',
    since       TIMESTAMPTZ NOT NULL,
    stuck       BOOLEAN NOT NULL DEFAULT false,
    unstaffed   BOOLEAN NOT NULL DEFAULT false,
    vars        JSONB NOT NULL DEFAULT '{}',
    loops       JSONB NOT NULL DEFAULT '{}',
    had         JSONB NOT NULL DEFAULT '{}',
    log         JSONB NOT NULL DEFAULT '[]',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A case that comes back continues its run; the database holds that line
-- too, so two control-plane calls racing on the same ticket cannot open two.
CREATE UNIQUE INDEX workflow_runs_open_case ON workflow_runs (workflow_id, case_ref)
    WHERE state NOT IN ('done', 'failed');
CREATE INDEX workflow_runs_by_workflow ON workflow_runs (workflow_id, seq);
