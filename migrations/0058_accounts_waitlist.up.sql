-- The account above the membership, and the codes that create it.
--
-- Up to here login was the same as membership: one row in humans carries
-- e-mail, password, organisation and role all at once. That rules out that a
-- person works in two organisations — and it rules out that a person exists at
-- all before their organisation does. Exactly that is what self-registration
-- needs: first the account, then joining or
-- founding.
--
-- humans therefore stays what it is — the seat in an organisation, to which
-- ten foreign keys point. Above it lies accounts, the login. The two are joined
-- when the session is moved onto the account (P1 in FR-002); until then
-- accounts stands on its own, and registration fills it.
--
-- feature-requests/002-plattform-registrierung.md
CREATE TABLE accounts (
    id                UUID PRIMARY KEY,
    email             TEXT NOT NULL UNIQUE, -- always stored lowercase
    password_hash     TEXT NOT NULL,
    display_name      TEXT NOT NULL DEFAULT '',
    -- NULL = not confirmed yet. As long as no mail delivery is configured,
    -- registration sets the timestamp right away: a confirmation that no
    -- one can send would be an account that nobody ever uses.
    email_verified_at TIMESTAMPTZ,
    -- The instance level, explicitly no organisation role: platform_admin
    -- grants every organisation to itself, system_admin nobody.
    platform_role     TEXT NOT NULL DEFAULT 'user'
                      CHECK (platform_role IN ('user','system_admin')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at     TIMESTAMPTZ
);

-- The waitlist code. Only its hash is stored, as with the sessions: whoever
-- reads the database gets no valid codes from it. The plaintext exists exactly
-- once, in the moment of creation.
--
-- What hangs on the code is stated not in code but here: how often it is
-- valid, until when, and whether it leads into a specific organisation (then
-- its holder joins instead of founding) or only applies to one e-mail domain.
CREATE TABLE waitlist_codes (
    code_hash     TEXT PRIMARY KEY,
    label         TEXT NOT NULL DEFAULT '',   -- "Konferenz X", "Pilotkunde Y"
    max_uses      INTEGER NOT NULL DEFAULT 1,
    used_count    INTEGER NOT NULL DEFAULT 0,
    expires_at    TIMESTAMPTZ,
    org_id        UUID REFERENCES organizations(id) ON DELETE CASCADE,
    email_pattern TEXT NOT NULL DEFAULT '',   -- e.g. "@firma.de"
    created_by    UUID REFERENCES humans(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at    TIMESTAMPTZ,
    CHECK (max_uses > 0)
);

-- Who redeemed which code. The primary key also keeps, beside that, the same
-- account from spending the same code twice.
CREATE TABLE waitlist_redemptions (
    code_hash   TEXT NOT NULL REFERENCES waitlist_codes(code_hash) ON DELETE CASCADE,
    account_id  UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    redeemed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (code_hash, account_id)
);
