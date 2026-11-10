-- Teams as a person (#712): a person lets one agent act in their own Teams
-- through delegated Microsoft Graph access. The refresh token is sealed with
-- the master key (associated data: the agent's id), never stored in the clear.
CREATE TABLE teamsuser_delegations (
    agent_id    UUID PRIMARY KEY REFERENCES agents(id) ON DELETE CASCADE,
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    human_id    UUID REFERENCES humans(id) ON DELETE CASCADE,
    -- The Microsoft account, as its ID token named it.
    ms_user     TEXT NOT NULL,
    tenant_id   TEXT NOT NULL,
    nonce       BYTEA NOT NULL,
    ciphertext  BYTEA NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX teamsuser_delegations_org ON teamsuser_delegations (org_id);
