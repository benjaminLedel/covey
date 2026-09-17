-- Egress allowlist: the target hosts maintained in the UI that sandboxes may
-- connect to outbound (in addition to the Anthropic hosts allowed in code
-- outright). Platform-global: the egress proxy is a single process and
-- cannot separate connections per organisation.
-- Patterns: exact host ("helpdesk.example.com") or wildcard ("*.example.com").
CREATE TABLE egress_allow (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pattern    TEXT NOT NULL UNIQUE,
    note       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
