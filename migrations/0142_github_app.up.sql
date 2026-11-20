-- GitHub App connections per organisation.
--
-- An organisation reaches GitHub either with a token it stores itself
-- (github_token, unchanged) or through a GitHub App: the platform's shared App
-- (configured by COVEY_GITHUB_APP_*) or an App of its own, created through
-- GitHub's manifest flow. Either way the control plane mints the installation
-- token; an own App's private key and webhook secret are sealed with the
-- master key (associated data: the organisation and the field) and never
-- become a secret an agent could be assigned.
CREATE TABLE github_app_connections (
    org_id           UUID PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    source           TEXT NOT NULL CHECK (source IN ('shared', 'own')),
    -- own App only (the shared App comes from the environment)
    app_id           BIGINT,
    app_slug         TEXT NOT NULL DEFAULT '',
    app_html_url     TEXT NOT NULL DEFAULT '',
    key_nonce        BYTEA,
    key_ct           BYTEA,
    hook_nonce       BYTEA,
    hook_ct          BYTEA,
    -- set once the App is installed; webhooks are routed by it
    installation_id  BIGINT UNIQUE,
    account_login    TEXT NOT NULL DEFAULT '',
    account_type     TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (source = 'shared' OR (app_id IS NOT NULL AND key_ct IS NOT NULL))
);
