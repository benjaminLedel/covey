# 18 — Runtimes & capacity (engines, seats, credential pools)

An organisation running a workforce of agents does not hold *one* LLM credential. It holds several subscription seats, one or two API keys, and before long credentials from more than one provider. Every one of them is a commercial arrangement with its own ceiling, its own price and its own semantics — and every agent has to sit on exactly one of them at a time.

This document describes how that is modelled: what a runtime *is* once there is more than one credential, how an agent gets assigned to one, how utilisation is measured rather than guessed, and what all of it does to the cost figures. The credential mechanics themselves (encryption, scoping, assignment) stay in [`04-identity-secrets.md`](04-identity-secrets.md); the cost side is spelled out in [`06-observability-control.md`](06-observability-control.md) and [`17-kpis.md`](17-kpis.md).

## Engine and runtime are two different things

Today "runtime" names two things at once, and as soon as an organisation holds several contracts the two drift apart. They are separated here:

- An **engine** is the code that drives the LLM loop: Claude Code, Codex, OpenHands. It is a plugin, it registers itself, it ships with the binary. There is one of each.
- A **runtime** is a *configured seat*: an engine plus the capacity to run it — credential, model choice, limits. It is data, it lives in the database, it has a name a human chose ("Claude subscription Ben", "Claude API production", "Codex team"), and it is what an agent is assigned to.

The distinction is not bookkeeping. "Which engine" is a technical property that follows from the work; "which contract does this agent work on" is a commercial and governance decision, made by a person, and one that has to be visible in the org chart. Collapsing them into one string forces the credential to be found by convention — which is exactly what stops working the moment a second provider or a second subscription appears.

The engine remains what [`01-architecture.md`](01-architecture.md) calls the runtime adapter, and it stays deliberately thin. What is added here sits above it.

## A runtime is engine + capacity

A runtime carries:

- the **engine** it runs on,
- one **or several credentials** — its capacity (see the pool in [`04-identity-secrets.md`](04-identity-secrets.md)),
- the **model** it uses, because that is a property of the contract and not of the agent (a subscription seat can afford the large model where a metered key cannot),
- its **limits**, in the sense given below.

A runtime belongs to **exactly one organisation**. It carries credentials, and a credential reaching across tenants would be a channel between them — the same reason a runner serves exactly one Covey instance and, within it, exactly one organisation ([`16-runner.md`](16-runner.md)). It is not only policy: the built-in secret store binds every ciphertext to its organisation through the AES-GCM AAD ([`04-identity-secrets.md`](04-identity-secrets.md)), so a shared credential would not decrypt for the second tenant in the first place. The schema follows the storage rather than fighting it.

The price is worth stating: a group with one provider contract and three subsidiary tenants deposits that credential three times, and its seats cannot be pooled across them. That is a **billing** problem, not a credential problem — costs can be rolled up across organisations later without any tenant ever holding another's secret, and that is the cheaper direction to solve it from.

Several credentials under one runtime are not redundancy, they are its size. An organisation with three subscription seats has one runtime with three values, not three runtimes — the seats are substitutable for the same work, and whoever administers them wants to think about "our Claude subscriptions" and not about three separate seats that happen to be interchangeable.

This is also why the pool hangs off the runtime and not off the secret's *name*. Two credentials of the same provider can live under different names (`claude_code_oauth_token` and `anthropic_api_key`) and still belong to the same pot; a pot cut along key names cannot express "use the subscriptions first, then the API key", because those halves are called different things.

## Two kinds of capacity, pulling in opposite directions

Capacity comes in two kinds, and confusing them produces the worst of both:

- **Quota** (a subscription seat). Paid for already, with a hard ceiling in a rolling window. The money is sunk. **Unused quota is wasted money** — you want to drive it towards full.
- **Metered** (an API key). Effectively unbounded, billed per use. **Every token costs** — you want to leave it as empty as possible.

Distributing load evenly across a mixed pool therefore optimises the wrong thing in both halves at once: every subscription ends up partly unused, and an API bill arrives anyway.

The correct policy is a **merit order**, as in dispatching a power grid: use the sunk-cost capacity first, and let the metered capacity cover only the peak. Within a runtime that order is literal — the sequence its credentials are written in, a statement somebody made on purpose rather than a heuristic nobody can see.

But the merit order applies **between unlike capacity only**. Within like capacity the opposite is right: three subscription seats are interchangeable and each has its own window, so stacking agents onto the first would let it hit its limit while the other two idle — and every agent that then dodges loses its prompt cache. Among equals the load is therefore **spread**, to the least loaded one, and at equal load to the one fewest agents sit on.

Neither rule fights the stickiness described below, because both govern where an agent is *placed*, and a placement is long-lived. Neither is re-evaluated per run.

## The human picks the contract, the platform picks the token

Two decisions, at two different speeds, and they must not be collapsed:

**Which runtime does this agent work on** is a human decision. It is commercial (whose budget), it is governance (the expensive contract is reserved for production agents; the QA fleet runs on a subscription), and it belongs next to the agent's other durable properties — visible, auditable, answerable from the org chart.

**Which of that runtime's credentials does this run use** is the platform's decision, made per waking phase, and described in [`04-identity-secrets.md`](04-identity-secrets.md): sticky, because the engine caches its prompt prefix per credential and a value swapped at every wake throws that cache away; moved only when the value is parked or has used up its share; returning to its home seat when that becomes healthy again.

So: **the human chooses the pot, the platform chooses the token.** A runtime holding exactly one credential and no fallback is a legitimate configuration, but it is one where an agent simply stops when that credential is limited — the elasticity comes from the pool, and giving it up is a choice that should be made knowingly.

### The first credential creates the first runtime

On an empty instance the two decisions collapse into one, and the setup makes that explicit: the very first credential an organisation enters is entered *as* a runtime — engine chosen from the registry, credential fields rendered from that engine's own declaration, checked once against the provider, and stored with the runtime created around it in the same step ([`20-hiring-and-setup.md`](20-hiring-and-setup.md)).

That is the reason the credential is the first question the platform asks at all. It is not one consumer's setting but three at once: the sandbox runs, the control plane's own LLM calls (config copilot, dream), and the agent that drafts other agents. Before it exists, nothing the interface offers can actually run; after it exists, most of the rest can be done for the person rather than by them.

### What an engine declares about its credentials

The engine, not the platform, knows which secret it needs and how it wants it. It declares them in order of precedence — an API key before a subscription token, so that an organisation holding both uses the one it is billed for deliberately rather than by accident. Each entry carries:

- the **secret name** it is looked up under, which is also the binding statement of intent (`anthropic_api_key` → an API key, `claude_code_oauth_token` → a subscription token). Never inferred from the value's prefix.
- the **kind** — metered or quota — from which the honest default unit of a limit follows: money where money is spent, the window quota where it is not.
- **how it is delivered.** Claude Code takes its credential as an environment variable; Codex takes an API key the same way but its ChatGPT-plan login as a **file** (`~/.codex/auth.json`, see [`19-codex-adapter.md`](19-codex-adapter.md)). An engine therefore declares an environment variable *or* a path in the agent home. Without that distinction the second engine's subscription case becomes a special case in the orchestrator — exactly what the registry exists to prevent.

The file form is subject to the same rule as the variable: written for the run, gone afterwards. A credential left lying in the home would be a long-lived secret in the sandbox ([`04-identity-secrets.md`](04-identity-secrets.md)).

### And what else an engine declares

The credential was the first thing that turned out not to be uniform across engines. It is not the last, and the pattern is always the same: something the platform assumed was universal is in fact a convention of the first engine.

- **Where materialised files go.** Covey writes an agent's skills into its home before every run ([`02-agent-model.md`](02-agent-model.md)); the path it writes to is Claude Code's convention (`~/.claude/skills/<name>/SKILL.md`), and another engine looks elsewhere or nowhere. So the engine declares the path — otherwise skills would silently do nothing on the second engine, which is the worst failure mode available: configured, visible in the interface, without effect.
- **What the engine can do.** Not every engine covers the whole agent model. The decisive one is **session resume**, on which the entire `blocked` mechanism rests ([`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)): an engine that cannot resume can carry agents that finish their work in one run, and cannot carry an agent that waits for an answer. That has to be a declared property, checked when an agent is assigned — a support agent placed on an engine without resume must be refused at assignment time, not discovered when the first customer query comes back.

Both follow the rule that makes the registry worth having: a difference between engines belongs in the engine's declaration, never in a conditional in the orchestrator.

Two admission questions sit on top and are deliberately kept apart:

- **Can it** — capability. An engine can only use credentials of the providers it speaks. This follows from the engine and is static.
- **May it** — entitlement. Which agents are allowed onto which runtime. This is governance and belongs with `ACCESS.md`, the guard rails and the budgets ([`06-observability-control.md`](06-observability-control.md), [`09-enterprise-model.md`](09-enterprise-model.md)).

## Utilisation: reported where possible, estimated where not

A limit is only worth as much as the measurement behind it. There are three sources, and they differ sharply in quality — the platform should use the best one available and say which one it used.

**Reported by the engine.** Some engines can be asked what their credential has consumed. Claude Code answers `/usage` headless and non-interactively, without a model turn (`num_turns: 0`, `total_cost_usd: 0`), with the figures that actually matter: the share of the current rolling window and of the week, plus the reset times. That is the provider's own number, not an inference from ours.

This belongs in the engine plugin as an **optional capability**, in the pattern the target systems already use twice for `WorkChecker`/`SignedWorkChecker`: an engine that can report utilisation implements the interface, one that cannot simply does not, and the platform falls back. It is executed by the daemon, because that is where the credential is; the control plane receives a number, not a binary.

Two properties have to be respected rather than wished away. The answer is **prose, not structured data** — the figures have to be read out of a human-readable text that will change between engine versions. That makes it a scraping dependency and it has to be handled fail-open, like the `nur-wenn:` check in [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md): a parser that no longer matches must leave the fleet running, never block it. And the reported number has a **scope** that needs verifying per engine — Claude Code notes that its contribution breakdown covers only local sessions on that machine, which for ephemeral per-agent sandboxes would mean each sandbox sees a fraction of the truth. Whether the headline window percentages are account-wide is the question that decides whether this source carries at all; it is cheap to settle and it is recorded as an open point below.

**Estimated by the platform.** Where nothing is reported, consumption is measured from what the platform itself booked: the costs of the runs that used that credential, in a rolling window ([`06-observability-control.md`](06-observability-control.md)). For a metered credential this is exact — it is real billing. For a quota credential it is a **proxy**: the money figure is notional there, and the token count is the closer stand-in for the provider's own counter. It steers; it does not measure. The interface has to label it as an estimate, because a bar drawn the same way in both cases claims a precision only one of them has.

**Observed at the ceiling.** When a credential is actually refused — a rate limit, an expired or revoked token — the platform learns something no estimate could tell it, and it beats every estimate. That signal parks the credential (see [`04-identity-secrets.md`](04-identity-secrets.md)) and, where an estimate is in play, it also **calibrates** it: at the moment of refusal the consumption booked in the window is an observed ceiling for that credential, revealed by the credential itself. Held on to, the next window is compared against a number that was witnessed rather than guessed.

## Limits are policy caps, not guesses at the ceiling

Once utilisation is reported, the configured limit stops being an attempt to guess the provider's ceiling and becomes something more useful: a **policy cap** on top of it. "This agent group may use at most 60% of this seat, the rest stays free for the humans on it" is a statement somebody *wants* to make. "I think this token is good for about twelve dollars an hour" was one they *had* to make.

Both are expressed the same way — an amount, a unit, a rolling window — but the unit follows the kind of capacity, and the engine knows which kind it is dealing with: money for metered credentials, where money is what is actually spent; the window quota for subscription seats, where money is notional.

## Scarcity is a scheduling input, not a broker error

An agent is not an interactive user. **A run can wait** — and that changes where a capacity shortage belongs.

Discovered at the broker, a shortage arrives too late: the decision to wake the agent has already been made, possibly a container has already been started. The honest place for capacity is as an **input to the dispatch decision** in [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md), next to "is there open work" and the heartbeat conditions: do not wake an agent whose runtime has nothing free, and wake one that can actually run instead.

A shortage then expresses itself as an **order** rather than as a delay, and across a fleet that is a real difference — the work does not sit still, it is done by whoever has capacity. A rate limit stops being a failed task and becomes a scheduling fact.

## What this does to the cost figures

**One figure, and it is named for what it is.** The engine reports a dollar amount per run, and the platform books it unchanged — for every credential, of either kind. There is deliberately no second cost model beside it, no fixed amount per seat entered by hand, no apportionment at period close.

What that figure *is*, though, has to be stated once and then carried in the labels, because it differs by credential kind:

> It is computed from token counts at **standard list prices**. On a metered credential that is what was billed. On a subscription seat it is a **list-price equivalent** — what the same work would have cost through the API. Nobody pays it; the seat costs what the seat costs.

Read as consumption, the number is sound in both cases and comparable across them: it says correctly how hard an agent made the machine work, whichever contract it ran on. Read as money spent, it is right on a metered credential and an overstatement on a seat.

The overstatement runs in the harmless direction for the question that matters most. An agent on a subscription seat looks *more* expensive than it is, so "is this workforce worth it" is answered conservatively rather than flatteringly. It runs the wrong way only for the marginal question — "should we hand it this next case as well" — where the honest answer on a seat with headroom is closer to nothing than to the reported figure. Where that question is actually being asked, the seat's utilisation is the figure to look at, not the price.

For an authoritative statement about money, the platform is the wrong place to ask: on a metered credential the provider's own billing view is the record, and on a subscription the record is the invoice for the seats. Covey reports consumption and attributes it; it does not reconcile a bill.

**And an engine that reports no figure at all is not hypothetical.** Codex reports token counts and leaves the pricing to the caller ([`19-codex-adapter.md`](19-codex-adapter.md)). So "book what the engine reports" holds only where an engine reports something, and the platform needs a **price list** — per model, per token kind — with which it prices the rest itself.

That is a lookup table, not a second cost model: it produces the same list-price equivalent that Claude Code computes locally, only computed one level further in. It is also why the token kinds are stored separately and not just the money ([`06-observability-control.md`](06-observability-control.md)) — the three are priced differently, and an engine's kinds may not map one to one onto ours.

A price list ages, and a wrong one is worse than an absent one because it looks like a measurement. It therefore belongs with the engine plugin, versioned with the binary, and a model it does not know has to yield **no figure** rather than a guessed one — a run whose price is unknown is honest; a run priced at zero is a lie in the direction nobody checks.

Cost must therefore be attributed not only to the agent and the task but to the **credential** the run went out on ([`06-observability-control.md`](06-observability-control.md)). Without that attribution there is no per-credential limit, no utilisation, and no answer to the question this whole model exists to make answerable: *is one seat too few, or one too many.*

## Driving an API directly — when that is a new engine, and when it is not

"Go straight through the provider's API" covers three different things, and only one of them is a new engine.

**A different credential on the same engine is not one.** Codex against an API key instead of a ChatGPT plan is the same binary, the same event stream, the same adapter — only another entry in the credential declaration above. Whoever builds a second engine for it maintains two adapters for one program and gets to keep both in step forever.

**A different provider behind the same engine is not one either**, where the engine supports it. Claude Code speaks to Bedrock, Vertex and Foundry as well as to Anthropic directly; that is a matter of which credential and which endpoint the engine is handed, not of a new adapter.

**Covey implementing the agent loop itself is a new engine — and one to think twice about.** What the existing engines provide is not the model call; it is everything around it: tool use, file editing, patch application, shell execution, context compaction, session persistence and resume, and a system prompt that has been tuned against real work. Rebuilding that means becoming a harness vendor, and it contradicts two of the design principles this platform rests on — the control plane is the product, and runtimes are swappable ([`README.md`](README.md), principles 2 and 3). Session resume in particular is not a detail: the whole `blocked` mechanism hangs off it ([`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)).

There is, however, a fourth case that is worth having and is often what is actually meant:

### The direct engine for work that needs no harness

Not every task an organisation gives an agent is agentic. Classify this ticket. Summarise this thread. Extract these fields. Such work needs **one model call**, no tools, no turns, no sandbox — and driving it through a coding harness pays for a system prompt, tool definitions and a multi-turn loop in order to answer a single question.

Covey already does this, it is just not modelled as an engine: `internal/claudeapi` is a narrow, tool-less path to the Messages API, used by the config copilot and by the dream ([`05-memory.md`](05-memory.md)). It is control-plane work rather than agent work, and it is wired to one provider.

Turning that into a **direct engine** would be tidy rather than ambitious: single turn, no tools, an answer instead of a run. It has three properties the harness engines do not:

- It needs **no sandbox**, so it can run in the control plane and is cheap enough for high-frequency work.
- It should be **provider-agnostic by construction** — Anthropic and OpenAI behind one interface — because there is nothing provider-specific about a single call, and that is exactly where a second provider costs nothing.
- Its cost is **exact**: the API returns token counts and the platform holds the price list, with no notional figure anywhere.

The distinction to keep is therefore not "CLI versus API" but **agentic versus not**. A harness engine drives work that needs tools and turns; the direct engine answers questions. Building the second one to do the first one's job is where this goes wrong.

Whether Covey ships a direct engine at all is [`07-open-decisions.md`](07-open-decisions.md), D14.

## Moving an agent: within an engine it is cheap, across engines it is not

Reassigning an agent to another runtime looks like one operation in the interface and is two very different ones underneath.

**Within the same engine** it is a credential swap. The agent notices nothing: its home is unchanged, a parked task resumes as before, only the seat it sits on is another. This is the case the merit order and the pool produce automatically, and it is safe to do at any run boundary.

**Across engines it is a change of job.** Three things do not travel:

- **A parked task.** A `blocked` task holds a session identifier belonging to the engine that created it. On another engine it cannot be resumed, so the task is orphaned — it has to be finished, or explicitly reset to `open` and started over, before the move.
- **The home.** `~/.claude` and `~/.codex` are different worlds: session transcripts, engine configuration, materialised skills. What the old engine left behind is dead weight to the new one, and what the new one needs is not there.
- **Behaviour.** The same `SOUL.md` compiles into a different system prompt and meets a different harness. The agent stays the same employee on paper and does not necessarily behave the same way.

None of this makes the move wrong — it makes it an operation that has to say so. An interface offering both as one dropdown implies an interchangeability that does not exist.

## The target data structure

Four questions, one table each. The credential *values* stay in the secret store; everything about *choosing* one sits here.

```sql
-- The contract. Named, because people talk about it ("Claude subscription team").
CREATE TABLE runtimes (
    id           UUID PRIMARY KEY,
    org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    engine       TEXT NOT NULL,              -- 'claude-code' | 'codex' | 'mock'
    display_name TEXT NOT NULL,
    model        TEXT NOT NULL DEFAULT '',   -- belongs to the contract, not the agent
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The capacity. ord IS the merit order: the paid-for seats first,
-- the metered fallback last.
CREATE TABLE runtime_credentials (
    runtime_id        UUID     NOT NULL REFERENCES runtimes(id) ON DELETE CASCADE,
    ord               SMALLINT NOT NULL,
    kind              TEXT     NOT NULL,  -- name from the engine's credential
                                          -- declaration; from it the engine knows
                                          -- env var vs. file, metered vs. quota
    secret_key        TEXT     NOT NULL,  -- pointer into the secret store
    secret_slot       SMALLINT NOT NULL,
    label             TEXT     NOT NULL DEFAULT '',    -- "subscription Ben"
    cooldown_until    TIMESTAMPTZ,
    cooldown_reason   TEXT     NOT NULL DEFAULT '',
    limit_amount      NUMERIC(14,4) NOT NULL DEFAULT 0,
    limit_unit        TEXT     NOT NULL DEFAULT 'usd',
    limit_window_secs INTEGER  NOT NULL DEFAULT 0,
    PRIMARY KEY (runtime_id, ord)
);

-- Who sits where. The stickiness.
CREATE TABLE runtime_bindings (
    runtime_id UUID     NOT NULL REFERENCES runtimes(id) ON DELETE CASCADE,
    agent_id   UUID     NOT NULL REFERENCES agents(id)   ON DELETE CASCADE,
    ord        SMALLINT NOT NULL,           -- the chosen credential
    home_ord   SMALLINT,                    -- home seat, while standing in elsewhere
    reason     TEXT     NOT NULL DEFAULT 'initial',
    bound_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (runtime_id, agent_id)
);

ALTER TABLE agents       ADD COLUMN runtime_id     UUID REFERENCES runtimes(id);
ALTER TABLE cost_entries ADD COLUMN runtime_id     UUID,
                         ADD COLUMN credential_ord SMALLINT;
```

`agents.runtime_id` replaces the engine name held as a string today — the assignment moves from "which framework" to "which contract", which is the whole point of the model.

### Storage below, policy above

The split this produces in the secret store is worth stating, because otherwise nobody will later reconstruct why the pool lives half here and half there.

**The secret store keeps exactly one thing from the pool: `slot`.** That a key can carry several values is a *storage* statement — encrypted, org-bound, listable — and it stays where the encryption, the AAD and the sensitivity rule already are. Building a second credential store for runtimes would mean having all three twice.

**Everything about choosing among those values moves up.** Stickiness, cooldown, limits and labels are capacity policy, not a property of a secret, and the current implementation shows it in three places: the selection has to be handed a usage function because the store does not own the data its own decision needs; the cooldown is triggered by an LLM API error, of which a secret store should know nothing; and the agent↔credential binding is orchestration living in the secrets schema.

So the port splits in two, along a line the implementation nearly draws already:

```go
// secrets — storage: which values may this agent use for key K?
Values(ctx, orgID, agentID, key) ([]Value, error)

// capacity — policy: which of them does it get right now?
Pick(runtime, agent) (Value, error)
```

The precedence rule (an agent's own secret before an assigned org secret) and the assignment check are *secret* concerns and stay below; the health and load decision goes up. `secret_assignments` likewise stays and keeps governing target-system secrets — for LLM credentials the assignment is now `agents.runtime_id`.

### Deliberately not in it

**No fixed cost per seat.** Rejected in [`07-open-decisions.md`](07-open-decisions.md) (D13); if it is ever wanted, `runtime_credentials` is the place, with an amount and a validity range.

**No merit order between runtimes.** A `position` column suggests itself and collides with the assignment: if a person assigns an agent to *one* runtime, nothing reads an order across them. Either the order stays inside a runtime — as described above — or agents are assigned to a *group* of runtimes and the order applies within the group. Left out until somebody actually has the case, rather than building a second selection layer nobody uses.

## An LLM credential is not a target-system token

Both are secrets, both can be held several times over, and the mechanics of picking one are identical — which is why they share an implementation ([`04-identity-secrets.md`](04-identity-secrets.md)). Their purpose is opposite, and the vocabulary should keep them apart:

- An **LLM credential is capacity.** It is fungible, invisible to the outside world, and more of it means more throughput.
- A **target-system token is an identity.** It appears in the audit trail and it carries permissions. More of them means more identities, not more capacity — and rotating between them *costs* traceability, because the same work then appears under three bot accounts.

Calling both "a pool" invites someone to eventually balance load across bot accounts, which is a governance regression dressed as an optimisation. Same machinery, different names.

## Where this stands, and what is open

Built: everything described above, apart from the two points below. The runtime as a named contract with its credentials in merit order, the assignment of agents to it, sticky selection with dodging and return, limits over a rolling window, the hard signal from a refused credential, deferral when nothing is free, cost attribution per credential, the engine's declaration of its credentials (environment variable or file) and its capabilities, reported utilisation via the engine, and the price list for engines that report no money.

The selection rule got a sharper shape while it was being built than this document originally gave it, and the difference matters: merit order applies between UNLIKE capacity (a paid-for seat before a metered key), and within LIKE capacity the load is spread instead. Three subscription seats are interchangeable and each has its own window — stacking agents onto the first would let it hit its limit while the others idle, and every agent that then dodges loses its prompt cache.

Not built: **capacity as a dispatch input**. A shortage is still noticed when the credential is brokered, so a wake happens and is then postponed, rather than the scheduler picking an agent that can actually run. And **Codex is declared but its run is unverified** ([`19-codex-adapter.md`](19-codex-adapter.md)) — above all whether it can resume a session, which is why its declaration says it cannot and the assignment refuses a blocking agent on it.

Decided: a runtime is **org-scoped** (see above). That was the one question here whose answer is a data question rather than a policy one; everything below can be retrofitted.

Open points, carried in [`07-open-decisions.md`](07-open-decisions.md):

- **What is the scope of a reported utilisation figure** per engine — account-wide, or only the machine that asked? For ephemeral sandboxes the difference decides whether the figure is usable at all.
- **How is a fixed cost entered and spread?** A monthly amount per runtime is the simple answer; whether that is enough for organisations with mid-period changes to their seat count is not settled.
