-- API keys: a second way of authenticating against the human API.
--
-- Until now there was exactly one — the browser session, an HttpOnly cookie
-- with SameSite=Strict. That is the right badge for a browser and a useless
-- one for everything else: a script, a pipeline, an agent skill that is
-- supposed to create an agent cannot get at it, and should not. Whoever wanted
-- to drive Covey from outside had no route at all, which is why the operational
-- tooling kept ending up beside the product instead of in it.
--
-- A key hangs off a SEAT, not merely off an account. Role and organisation are
-- properties of the membership; an account can hold several, and a credential
-- that did not say which one it works from would have an authority nobody can
-- name. The cascade follows from that: whoever loses the seat loses the key
-- with it — rights that outlive their seat are the classic hole.
--
-- Only the hash is stored. The token is shown once, at creation, and never
-- again; the prefix is what makes a key recognisable in the list afterwards.
CREATE TABLE api_keys (
    id           uuid PRIMARY KEY,
    account_id   uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    human_id     uuid NOT NULL REFERENCES humans(id) ON DELETE CASCADE,
    name         text NOT NULL,
    prefix       text NOT NULL,
    token_hash   text NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    expires_at   timestamptz
);

CREATE INDEX api_keys_account_idx ON api_keys (account_id, created_at DESC);
