-- A secret key may carry several values (a "pool").
--
-- The trigger is the Claude Code tokens: whoever holds several subscription
-- seats wants to spread them over the agents, instead of pushing all agents
-- through one token and landing in the limit. The same holds for bot accounts
-- at GitLab/GitHub — that is why the pool sits under EVERY key and not in a
-- special case for Anthropic.
--
-- slot numbers the values of a key. All existing rows become slot 0, so
-- today's behaviour is kept word for word: one key, one value. The
-- assignment to agents (secret_assignments) deliberately stays at KEY
-- level — which slot it then becomes is decided by the selection, not by
-- the administration. Otherwise every new token would mean pulling all
-- assignments along.
ALTER TABLE secrets
    ADD COLUMN slot  SMALLINT NOT NULL DEFAULT 0,
    ADD COLUMN label TEXT     NOT NULL DEFAULT '';

-- The state of a single value. cooldown_until IS NULL means: healthy and
-- selectable. It is set from two directions — from the soft limit below
-- (consumption in the window exhausted) and from the hard signal (the API did
-- reject the token: 429, expired, revoked).
ALTER TABLE secrets
    ADD COLUMN cooldown_until  TIMESTAMPTZ,
    ADD COLUMN cooldown_reason TEXT NOT NULL DEFAULT '';

-- The configured limit of a value, in a rolling window.
--
-- Two units, because "consumption" is something different per credential: for
-- an API key it is money (usd, the number from cost_entries is real billing
-- there), for a subscription token money is a fiction — one does not pay it,
-- the real limit is Anthropic's rolling window. There tokens count as the
-- approximation. The approximation only DRIVES; the truth stays the hard signal.
--
-- limit_window_secs = 0 means: no limit. That is the default and therefore the
-- state of every existing value.
ALTER TABLE secrets
    ADD COLUMN limit_amount      NUMERIC(14,4) NOT NULL DEFAULT 0,
    ADD COLUMN limit_unit        TEXT          NOT NULL DEFAULT 'usd',
    ADD COLUMN limit_window_secs INTEGER       NOT NULL DEFAULT 0;

ALTER TABLE secrets ADD CONSTRAINT secrets_limit_unit_chk
    CHECK (limit_unit IN ('usd', 'tokens'));

-- A secret is only unique now together with the slot. The two partial indexes
-- from 0007 are redrawn for that; their role (org-wide vs. agent-owned) stays
-- unchanged.
DROP INDEX uq_secrets_org;
DROP INDEX uq_secrets_agent;
CREATE UNIQUE INDEX uq_secrets_org   ON secrets (org_id, key, slot) WHERE agent_id IS NULL;
CREATE UNIQUE INDEX uq_secrets_agent ON secrets (org_id, agent_id, key, slot) WHERE agent_id IS NOT NULL;

-- Who sits on which value.
--
-- Deliberately a stored binding instead of a computed one (say
-- hash(agent_id) % count): adding one more token would reroll ALL agents
-- through a modulo selection. Every agent would get a different credential, and
-- because Claude Code caches the prompt prefix per credential, every cache would
-- be cold at once — a side effect of an addition that should hurt nobody. A
-- stored binding survives changes to the pool, a new value is taken only by new
-- agents and by agents that moved off.
--
-- home_slot is the original seat: set for as long as the agent moved off. That
-- makes the return targeted once the original seat is healthy again, instead of
-- redistributing on every evaluation.
CREATE TABLE secret_bindings (
    org_id    UUID     NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    key       TEXT     NOT NULL,
    agent_id  UUID     NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    slot      SMALLINT NOT NULL,
    home_slot SMALLINT,
    reason    TEXT     NOT NULL DEFAULT 'initial',
    bound_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, key, agent_id)
);

-- The cost attribution to the value the run ran on.
--
-- Without these two columns no limit per token is measurable, no utilisation is
-- displayable, and the question "is another seat worth it" cannot be answered:
-- cost_entries knew only agent, task and model so far.
--
-- Nullable, and the existing rows stay NULL — from which token a past
-- run was paid cannot be reconstructed. The evaluation must live with
-- old rows carrying no attribution; they still count into the total
-- cost, only not into the breakdown per value.
ALTER TABLE cost_entries
    ADD COLUMN secret_key  TEXT,
    ADD COLUMN secret_slot SMALLINT;

-- The index the window query runs on. It runs on EVERY selection (once
-- per run, for every value with a configured limit), so it has to be
-- cheap. Partial, because the existing rows and every run without
-- attribution contribute nothing.
CREATE INDEX idx_cost_secret_slot
    ON cost_entries (secret_key, secret_slot, created_at)
    WHERE secret_key IS NOT NULL;
