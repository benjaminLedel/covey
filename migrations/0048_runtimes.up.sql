-- The runtime as a contract: the engine plus the capacity to run it.
--
-- So far agents.runtime held a framework name ("claude-code"), and the
-- credential was found by a hard-coded naming convention
-- (anthropic_api_key, then claude_code_oauth_token). That holds exactly as long
-- as there is one provider and one account.
--
-- Once an organisation holds several contracts — three subscription seats and
-- an API key, later ChatGPT too —, "which runtime" is no longer a technical
-- property but a commercial one: whose contract does this employee work on.
-- That is a decision a human makes, and one the org chart has to be able to
-- answer. Details in spec/18-runtimes-capacity.md.

-- WHAT kind of workplace.
--
-- org_id NOT NULL: a runtime carries credentials, and a credential across
-- tenants would be a channel between them — the same reason spec/16 gives
-- for one runner serving exactly one Covey instance. The store had decided
-- this anyway: the AES-GCM AAD binds every ciphertext to its
-- organisation (D13).
CREATE TABLE runtimes (
    id           UUID PRIMARY KEY,
    org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    engine       TEXT NOT NULL,
    display_name TEXT NOT NULL,
    -- The model belongs to the contract, not to the agent: a subscription seat
    -- can afford the big model where a metered key cannot. Empty = the engine's
    -- default.
    model        TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_runtimes_org ON runtimes (org_id, display_name);

-- WHAT it works with. ord IS the merit order.
--
-- The two kinds of capacity pull against each other: a subscription is paid
-- for, unused quota is burnt money, you want to run it full. An API key
-- costs per token, you want to leave it empty. An even split across both
-- delivers the worst of both. Right is a merit order like power-plant
-- dispatch — and here it is plainly the order someone wrote down, not a
-- hidden heuristic.
--
-- kind is the name from the credential declaration of the engine (api_key,
-- subscription). From it the engine knows whether the value arrives as an
-- ENV variable or as a file, and whether metered or quota applies.
CREATE TABLE runtime_credentials (
    runtime_id        UUID     NOT NULL REFERENCES runtimes(id) ON DELETE CASCADE,
    ord               SMALLINT NOT NULL,
    kind              TEXT     NOT NULL,
    -- Pointer into the secret store. The value itself stays there: encryption,
    -- AAD and the `sensitive` rule live there, and holding them a second time
    -- would be the most expensive route to the same result.
    secret_key        TEXT     NOT NULL,
    secret_slot       SMALLINT NOT NULL DEFAULT 0,
    label             TEXT     NOT NULL DEFAULT '',
    -- State: NULL = healthy and selectable. It is set from two directions — the
    -- soft limit below and the hard signal (the API actually rejected the
    -- value).
    cooldown_until    TIMESTAMPTZ,
    cooldown_reason   TEXT     NOT NULL DEFAULT '',
    -- The cap in a rolling window. Since engines can report their utilisation,
    -- the field no longer guesses the provider's ceiling, it caps it
    -- politically ("this group may use at most 60 % of the seat").
    -- limit_window_secs = 0 means: no limit.
    limit_amount      NUMERIC(14,4) NOT NULL DEFAULT 0,
    limit_unit        TEXT          NOT NULL DEFAULT 'usd',
    limit_window_secs INTEGER       NOT NULL DEFAULT 0,
    PRIMARY KEY (runtime_id, ord),
    CONSTRAINT runtime_credentials_unit_chk CHECK (limit_unit IN ('usd', 'tokens'))
);

-- WHO sits on which credential.
--
-- Deliberately stored and not computed (say hash(agent_id) % count): when
-- another token is added, a modulo choice re-rolls ALL agents, and because
-- the engine caches the prompt prefix per credential, every cache would go
-- cold at one stroke — a side effect of an addition that should hurt
-- nobody.
--
-- home_ord is the home seat: set while the agent has moved away from it. That
-- makes the return deliberate as soon as the home seat is healthy again,
-- instead of redistributing on every choice.
CREATE TABLE runtime_bindings (
    runtime_id UUID     NOT NULL REFERENCES runtimes(id) ON DELETE CASCADE,
    agent_id   UUID     NOT NULL REFERENCES agents(id)   ON DELETE CASCADE,
    ord        SMALLINT NOT NULL,
    home_ord   SMALLINT,
    reason     TEXT     NOT NULL DEFAULT 'initial',
    bound_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (runtime_id, agent_id)
);

ALTER TABLE agents ADD COLUMN runtime_id UUID REFERENCES runtimes(id) ON DELETE SET NULL;

-- Cost attribution moves one level up: no longer on (key, slot), but on
-- (runtime, credential). The existing stock keeps its old columns and stays
-- NULL here — from which contract a past run was paid cannot be
-- reconstructed, and an invented attribution would be worse than a missing
-- one.
ALTER TABLE cost_entries
    ADD COLUMN runtime_id     UUID,
    ADD COLUMN credential_ord SMALLINT;
CREATE INDEX idx_cost_runtime_cred
    ON cost_entries (runtime_id, credential_ord, created_at)
    WHERE runtime_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Migrate the existing data.
--
-- For every organisation and every engine actually used there, one runtime
-- is created. Its name is the engine name; whoever wants to keep several
-- contracts apart creates more afterwards and reassigns.
INSERT INTO runtimes (id, org_id, engine, display_name)
SELECT gen_random_uuid(), a.org_id, a.runtime, a.runtime
FROM agents a
GROUP BY a.org_id, a.runtime;

UPDATE agents a SET runtime_id = r.id
FROM runtimes r WHERE r.org_id = a.org_id AND r.engine = a.runtime;

-- The known LLM keys become credentials of the matching runtime, in the
-- order of the previous precedence rule (API key before subscription token).
-- Cooldown, limit and label move along unchanged.
INSERT INTO runtime_credentials
    (runtime_id, ord, kind, secret_key, secret_slot, label,
     cooldown_until, cooldown_reason, limit_amount, limit_unit, limit_window_secs)
SELECT r.id,
       (ROW_NUMBER() OVER (PARTITION BY r.id
            ORDER BY CASE s.key WHEN 'anthropic_api_key' THEN 0 ELSE 1 END, s.slot) - 1)::SMALLINT,
       CASE s.key WHEN 'anthropic_api_key' THEN 'api_key' ELSE 'subscription' END,
       s.key, s.slot, s.label,
       s.cooldown_until, s.cooldown_reason, s.limit_amount, s.limit_unit, s.limit_window_secs
FROM secrets s
JOIN runtimes r ON r.org_id = s.org_id AND r.engine = 'claude-code'
WHERE s.agent_id IS NULL AND s.key IN ('anthropic_api_key', 'claude_code_oauth_token');

-- The seat occupancy moves along too, so that nobody loses their place
-- through the migration and all prompt caches go cold at once.
INSERT INTO runtime_bindings (runtime_id, agent_id, ord, home_ord, reason, bound_at)
SELECT rc.runtime_id, b.agent_id, rc.ord, home.ord, b.reason, b.bound_at
FROM secret_bindings b
JOIN agents a ON a.id = b.agent_id AND a.org_id = b.org_id
JOIN runtime_credentials rc
     ON rc.runtime_id = a.runtime_id AND rc.secret_key = b.key AND rc.secret_slot = b.slot
LEFT JOIN runtime_credentials home
     ON home.runtime_id = a.runtime_id AND home.secret_key = b.key AND home.secret_slot = b.home_slot
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- And what becomes of secrets: a store, nothing else.
--
-- Of everything the pool hung on the table, exactly one column remains
-- — slot. That one key can carry several values is a statement about
-- storage and belongs next to encryption, AAD and the `sensitive` rule.
-- The choice among them (stickiness, cooldown, limits) is capacity
-- policy and now lives above. The old placement showed its wrong level: the
-- choice was handed a consumption function because the store did not have the
-- data for its own decision, and its cooldown was triggered by an LLM API
-- error that a secret store should know nothing about.
DROP TABLE secret_bindings;
ALTER TABLE secrets DROP CONSTRAINT secrets_limit_unit_chk;
ALTER TABLE secrets
    DROP COLUMN label,
    DROP COLUMN cooldown_until,
    DROP COLUMN cooldown_reason,
    DROP COLUMN limit_amount,
    DROP COLUMN limit_unit,
    DROP COLUMN limit_window_secs;
