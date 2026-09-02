-- One-time tokens for an address: the confirmation link and the password
-- reset (#168).
--
-- Only the hash is stored, as with sessions and waitlist codes: whoever reads
-- the database gets no valid link out of it. The clear text exists exactly
-- once — in the mail — which is what makes the mail the proof that the address
-- belongs to whoever typed it.
--
-- Both purposes in one table, discriminated by `purpose`, because they differ
-- in nothing that matters here: a hash, an account, an expiry, and one use.
-- Two tables would mean two redemption paths, and the redemption is the part
-- that has to be exactly right.
--
-- No unique constraint per account: a second confirmation mail must not have
-- to invalidate the first. Whoever presses "send again" because the mail was
-- slow would otherwise hold two links of which only the newer works, and
-- would click the older one.
--
-- feature-requests/002-plattform-registrierung.md
CREATE TABLE account_tokens (
    token_hash TEXT PRIMARY KEY,
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    purpose    TEXT NOT NULL CHECK (purpose IN ('verify','reset')),
    expires_at TIMESTAMPTZ NOT NULL,
    -- NULL = unused. Set on redemption instead of deleting the row: a link
    -- that was already used has to be distinguishable from one that never
    -- existed, otherwise "this link has expired" is the answer to a double
    -- click on the same mail.
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- For the cleanup and for "does an unused token already exist".
CREATE INDEX account_tokens_account_idx ON account_tokens (account_id, purpose, used_at);
