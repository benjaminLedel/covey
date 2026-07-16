-- Covey Grundschema: Organisation, Menschen (RBAC), Agenten, Config, Backlog,
-- Recording, Kosten, Secrets, Guard-Rails, Approvals, Webhook-Idempotenz.

CREATE TABLE organizations (
    id          UUID PRIMARY KEY,
    name        TEXT NOT NULL,
    fleet_killed BOOLEAN NOT NULL DEFAULT FALSE, -- flottenweiter Kill-Switch
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE humans (
    id            UUID PRIMARY KEY,
    org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email         TEXT NOT NULL UNIQUE,
    display_name  TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('platform_admin','agent_owner','security','auditor','controlling')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE http_sessions (
    token_hash  TEXT PRIMARY KEY,
    human_id    UUID NOT NULL REFERENCES humans(id) ON DELETE CASCADE,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE agents (
    id            UUID PRIMARY KEY,
    org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    slug          TEXT NOT NULL,
    display_name  TEXT NOT NULL,
    runtime       TEXT NOT NULL DEFAULT 'claude-code',
    status        TEXT NOT NULL DEFAULT 'sleeping'
                  CHECK (status IN ('sleeping','triggered','triage','working','killed')),
    owner_id      UUID REFERENCES humans(id) ON DELETE SET NULL,
    supervisor    TEXT NOT NULL DEFAULT '', -- Org-Chart: an wen eskaliert wird
    killed        BOOLEAN NOT NULL DEFAULT FALSE,
    budget_usd    NUMERIC(12,4) NOT NULL DEFAULT 0, -- 0 = kein Deckel
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, slug)
);

-- Config-as-Code: jede Änderung ist eine neue Version (Audit gratis).
CREATE TABLE agent_config_versions (
    id              UUID PRIMARY KEY,
    agent_id        UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    version         INTEGER NOT NULL,
    files           JSONB NOT NULL, -- {"SOUL.md": "...", "ACCESS.md": "...", ...}
    compiled_prompt TEXT NOT NULL,
    created_by      UUID REFERENCES humans(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (agent_id, version)
);

-- Zugänge aus ACCESS.md, materialisiert für den Broker (Referenzen, nie Secrets).
CREATE TABLE system_accesses (
    agent_id  UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    system    TEXT NOT NULL,
    scopes    TEXT[] NOT NULL DEFAULT '{}',
    PRIMARY KEY (agent_id, system)
);

CREATE TABLE backlog_tasks (
    id                 UUID PRIMARY KEY,
    org_id             UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id           UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    title              TEXT NOT NULL,
    body               TEXT NOT NULL DEFAULT '',
    state              TEXT NOT NULL DEFAULT 'open'
                       CHECK (state IN ('open','in_progress','blocked','done','failed','cancelled')),
    priority           INTEGER NOT NULL DEFAULT 5, -- kleiner = wichtiger
    origin             TEXT NOT NULL DEFAULT 'manual', -- manual | webhook:zammad | schedule | agent:<id>
    correlation_key    TEXT,          -- gesetzt, solange state='blocked'
    runtime_session_id TEXT,          -- Claude-Code session_id für --resume
    resume_input       TEXT,          -- Eingabe für die Wiederaufnahme (z. B. Kundenantwort)
    result             TEXT,
    error              TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_backlog_agent_state ON backlog_tasks (agent_id, state, priority, created_at);
CREATE INDEX idx_backlog_correlation ON backlog_tasks (correlation_key) WHERE state = 'blocked';

CREATE TABLE task_transitions (
    id         BIGSERIAL PRIMARY KEY,
    task_id    UUID NOT NULL REFERENCES backlog_tasks(id) ON DELETE CASCADE,
    from_state TEXT NOT NULL,
    to_state   TEXT NOT NULL,
    note       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Session-Recording: unveränderlich, append-only.
CREATE TABLE recording_events (
    id         BIGSERIAL PRIMARY KEY,
    org_id     UUID NOT NULL,
    agent_id   UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    task_id    UUID,
    kind       TEXT NOT NULL, -- runtime | tool_use | credential | approval | guardrail | lifecycle | action
    payload    JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_recording_agent ON recording_events (agent_id, id);
CREATE INDEX idx_recording_task ON recording_events (task_id, id);

CREATE TABLE cost_entries (
    id            BIGSERIAL PRIMARY KEY,
    agent_id      UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    task_id       UUID,
    usd           NUMERIC(12,6) NOT NULL DEFAULT 0,
    input_tokens  BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    model         TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_cost_agent ON cost_entries (agent_id, created_at);

-- Built-in SecretStore: AES-GCM-verschlüsselte Werte, Master-Key aus ENV.
CREATE TABLE secrets (
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    nonce      BYTEA NOT NULL,
    ciphertext BYTEA NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, key)
);

-- Guard-Rails: zentral, versionierbar, additiv-restriktiv, fail-closed.
CREATE TABLE guardrails (
    id          UUID PRIMARY KEY,
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    scope_level TEXT NOT NULL CHECK (scope_level IN ('global','team','agent')),
    agent_id    UUID REFERENCES agents(id) ON DELETE CASCADE, -- nur bei scope_level='agent'
    rule_type   TEXT NOT NULL CHECK (rule_type IN ('deny_system','deny_action','require_approval','budget_limit')),
    pattern     TEXT NOT NULL, -- System-/Action-Muster, z. B. 'zammad:reply_external' oder 'mail:*'
    params      JSONB NOT NULL DEFAULT '{}',
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    created_by  UUID REFERENCES humans(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE approvals (
    id           UUID PRIMARY KEY,
    org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id     UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    task_id      UUID,
    action       TEXT NOT NULL,
    params       JSONB NOT NULL DEFAULT '{}',
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','denied','expired')),
    used         BOOLEAN NOT NULL DEFAULT FALSE, -- erteilte Freigabe einmalig konsumierbar
    requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at   TIMESTAMPTZ,
    decided_by   UUID REFERENCES humans(id) ON DELETE SET NULL
);
CREATE INDEX idx_approvals_pending ON approvals (org_id, status, requested_at);

-- Idempotenz für eingehende Webhooks (Zammad wiederholt bis zu 4×).
CREATE TABLE webhook_events (
    dedup_key   TEXT PRIMARY KEY,
    source      TEXT NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
