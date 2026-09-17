-- Registration of foreign runners (spec/16).
--
-- Split in two like GitLab: one registration token per organisation, which is
-- created and revoked in the UI, and derived from it a
-- long-lived runner token per runner. Only the hash of both is stored.
--
-- The registration token carries the organisation, and the runner inherits it
-- from it. It cannot change it: a runner holds homes and daemon tokens,
-- and both are the property of exactly one tenant.
CREATE TABLE runner_registration_tokens (
    id          UUID PRIMARY KEY,
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_by  UUID REFERENCES humans(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Revocation instead of deletion: whoever wants to know with which token a
    -- runner came in should still be able to answer that after the token
    -- became invalid.
    revoked_at  TIMESTAMPTZ
);
CREATE INDEX idx_runner_reg_tokens_org ON runner_registration_tokens (org_id, created_at DESC);

-- Where a runner comes from and what it can do. All added afterwards, because
-- 0051 only needed the identity that the egress proxy reports against.
ALTER TABLE runners
    ADD COLUMN description TEXT NOT NULL DEFAULT '',
    ADD COLUMN tags        TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN version     TEXT NOT NULL DEFAULT '',
    ADD COLUMN arch        TEXT NOT NULL DEFAULT '',
    -- The protocol version it speaks. Runner and server are shipped
    -- separately, so different versions necessarily meet; the
    -- view should be able to show the offset instead of letting it be guessed.
    ADD COLUMN protocol    INTEGER NOT NULL DEFAULT 0;
