-- Single sign-on per organisation (#723).
--
-- An organisation brings its own OIDC provider; covey is the relying party.
-- The client secret is sealed with the master key (associated data: the
-- organisation's id). required: password sign-in is refused for the
-- organisation's verified domains, and the accounts of those domains lose
-- their password.
CREATE TABLE org_sso (
    org_id        UUID PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    issuer        TEXT NOT NULL,
    client_id     TEXT NOT NULL,
    secret_nonce  BYTEA NOT NULL,
    secret_ct     BYTEA NOT NULL,
    default_role  TEXT NOT NULL,
    required      BOOLEAN NOT NULL DEFAULT false,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The e-mail domains an organisation claims. A domain counts once a DNS TXT
-- record _covey-challenge.<domain> carries the token; until then it routes
-- nobody anywhere. One organisation per domain on the instance — the primary
-- key says so, verified or not, so a second claim cannot sit waiting.
CREATE TABLE org_sso_domains (
    domain       TEXT PRIMARY KEY,           -- lowercase, no leading @
    org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    token        TEXT NOT NULL,
    verified_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX org_sso_domains_org ON org_sso_domains (org_id);

-- Which provider identity signs in as which account: issuer and subject, not
-- the e-mail address, which a provider may reassign.
CREATE TABLE account_identities (
    issuer         TEXT NOT NULL,
    subject        TEXT NOT NULL,
    account_id     UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    org_id         UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at  TIMESTAMPTZ,
    PRIMARY KEY (issuer, subject)
);
CREATE INDEX account_identities_account ON account_identities (account_id);
