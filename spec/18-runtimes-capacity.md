# 18 — Runtimes & capacity (engines, workplaces, credential pools)

An organisation running a workforce of agents does not hold *one* LLM credential. It holds several subscription seats, one or two API keys, and before long credentials from more than one provider. Every one of them is a commercial arrangement with its own ceiling, its own price and its own semantics — and every agent has to sit on exactly one of them at a time.

This document describes how that is modelled: what a runtime *is* once there is more than one credential, how an agent gets assigned to one, how utilisation is measured rather than guessed, and what all of it does to the cost figures. The credential mechanics themselves (encryption, scoping, assignment) stay in [`04-identity-secrets.md`](04-identity-secrets.md); the cost side is spelled out in [`06-observability-control.md`](06-observability-control.md) and [`17-kpis.md`](17-kpis.md).

## Engine and runtime are two different things

Today "runtime" names two things at once, and as soon as an organisation holds several contracts the two drift apart. They are separated here:

- An **engine** is the code that drives the LLM loop: Claude Code, Codex, OpenHands. It is a plugin, it registers itself, it ships with the binary. There is one of each.
- A **runtime** is a *configured workplace*: an engine plus the capacity to run it — credential, model choice, limits. It is data, it lives in the database, it has a name a human chose ("Claude subscription Ben", "Claude API production", "Codex team"), and it is what an agent is assigned to.

The distinction is not bookkeeping. "Which engine" is a technical property that follows from the work; "which contract does this agent work on" is a commercial and governance decision, made by a person, and one that has to be visible in the org chart. Collapsing them into one string forces the credential to be found by convention — which is exactly what stops working the moment a second provider or a second subscription appears.

The engine remains what [`01-architecture.md`](01-architecture.md) calls the runtime adapter, and it stays deliberately thin. What is added here sits above it.

## A runtime is engine + capacity

A runtime carries:

- the **engine** it runs on,
- one **or several credentials** — its capacity (see the pool in [`04-identity-secrets.md`](04-identity-secrets.md)),
- the **model** it uses, because that is a property of the contract and not of the agent (a subscription seat can afford the large model where a metered key cannot),
- its **limits**, in the sense given below.

A runtime belongs to **exactly one organisation**. It carries credentials, and a credential reaching across tenants would be a channel between them — the same reason a runner serves exactly one Covey instance ([`16-runner.md`](16-runner.md)). It is not only policy: the built-in secret store binds every ciphertext to its organisation through the AES-GCM AAD ([`04-identity-secrets.md`](04-identity-secrets.md)), so a shared credential would not decrypt for the second tenant in the first place. The schema follows the storage rather than fighting it.

The price is worth stating: a group with one provider contract and three subsidiary tenants deposits that credential three times, and its seats cannot be pooled across them. That is a **billing** problem, not a credential problem — costs can be rolled up across organisations later without any tenant ever holding another's secret, and that is the cheaper direction to solve it from.

Several credentials under one runtime are not redundancy, they are its size. An organisation with three subscription seats has one runtime with three values, not three runtimes — the seats are substitutable for the same work, and whoever administers them wants to think about "our Claude subscriptions" and not about three separate workplaces that happen to be interchangeable.

This is also why the pool hangs off the runtime and not off the secret's *name*. Two credentials of the same provider can live under different names (`claude_code_oauth_token` and `anthropic_api_key`) and still belong to the same pot; a pot cut along key names cannot express "use the subscriptions first, then the API key", because those halves are called different things.

## Two kinds of capacity, pulling in opposite directions

Capacity comes in two kinds, and confusing them produces the worst of both:

- **Quota** (a subscription seat). Paid for already, with a hard ceiling in a rolling window. The money is sunk. **Unused quota is wasted money** — you want to drive it towards full.
- **Metered** (an API key). Effectively unbounded, billed per use. **Every token costs** — you want to leave it as empty as possible.

Distributing load evenly across a mixed pool therefore optimises the wrong thing in both halves at once: every subscription ends up partly unused, and an API bill arrives anyway.

The correct policy is a **merit order**, as in dispatching a power grid: load the sunk-cost capacity to its ceiling first, in order, and let the metered capacity cover only the peak. Applied to runtimes this is pleasantly literal — the order in which an organisation lists its runtimes *is* the merit order, and it is a statement somebody made on purpose rather than a heuristic nobody can see.

The merit order does not fight the stickiness described below, because it governs **assignment**, and an assignment is long-lived. It is not re-evaluated per run.

## The human picks the contract, the platform picks the token

Two decisions, at two different speeds, and they must not be collapsed:

**Which runtime does this agent work on** is a human decision. It is commercial (whose budget), it is governance (the expensive contract is reserved for production agents; the QA fleet runs on a subscription), and it belongs next to the agent's other durable properties — visible, auditable, answerable from the org chart.

**Which of that runtime's credentials does this run use** is the platform's decision, made per waking phase, and described in [`04-identity-secrets.md`](04-identity-secrets.md): sticky, because the engine caches its prompt prefix per credential and a value swapped at every wake throws that cache away; moved only when the value is parked or has used up its share; returning to its home seat when that becomes healthy again.

So: **the human chooses the pot, the platform chooses the token.** A runtime holding exactly one credential and no fallback is a legitimate configuration, but it is one where an agent simply stops when that credential is limited — the elasticity comes from the pool, and giving it up is a choice that should be made knowingly.

### What an engine declares about its credentials

The engine, not the platform, knows which secret it needs and how it wants it. It declares them in order of precedence — an API key before a subscription token, so that an organisation holding both uses the one it is billed for deliberately rather than by accident. Each entry carries:

- the **secret name** it is looked up under, which is also the binding statement of intent (`anthropic_api_key` → an API key, `claude_code_oauth_token` → a subscription token). Never inferred from the value's prefix.
- the **kind** — metered or quota — from which the honest default unit of a limit follows: money where money is spent, the window quota where it is not.
- **how it is delivered.** Claude Code takes its credential as an environment variable; Codex takes an API key the same way but its ChatGPT-plan login as a **file** (`~/.codex/auth.json`, see [`19-codex-adapter.md`](19-codex-adapter.md)). An engine therefore declares an environment variable *or* a path in the agent home. Without that distinction the second engine's subscription case becomes a special case in the orchestrator — exactly what the registry exists to prevent.

The file form is subject to the same rule as the variable: written for the run, gone afterwards. A credential left lying in the home would be a long-lived secret in the sandbox ([`04-identity-secrets.md`](04-identity-secrets.md)).

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

Covey already does this, it is just not modelled as an engine: `internal/claudeapi` is a narrow, tool-less path to the Messages API, used by the config copilot and by the dream. It is control-plane work rather than agent work, and it is wired to one provider.

Turning that into a **direct engine** would be tidy rather than ambitious: single turn, no tools, an answer instead of a run. It has three properties the harness engines do not:

- It needs **no sandbox**, so it can run in the control plane and is cheap enough for high-frequency work.
- It should be **provider-agnostic by construction** — Anthropic and OpenAI behind one interface — because there is nothing provider-specific about a single call, and that is exactly where a second provider costs nothing.
- Its cost is **exact**: the API returns token counts and the platform holds the price list, with no notional figure anywhere.

The distinction to keep is therefore not "CLI versus API" but **agentic versus not**. A harness engine drives work that needs tools and turns; the direct engine answers questions. Building the second one to do the first one's job is where this goes wrong.

Whether Covey ships a direct engine at all is [`07-open-decisions.md`](07-open-decisions.md), D14.

## An LLM credential is not a target-system token

Both are secrets, both can be held several times over, and the mechanics of picking one are identical — which is why they share an implementation ([`04-identity-secrets.md`](04-identity-secrets.md)). Their purpose is opposite, and the vocabulary should keep them apart:

- An **LLM credential is capacity.** It is fungible, invisible to the outside world, and more of it means more throughput.
- A **target-system token is an identity.** It appears in the audit trail and it carries permissions. More of them means more identities, not more capacity — and rotating between them *costs* traceability, because the same work then appears under three bot accounts.

Calling both "a pool" invites someone to eventually balance load across bot accounts, which is a governance regression dressed as an optimisation. Same machinery, different names.

## Where this stands, and what is open

Built today: the credential pool with sticky per-agent assignment, soft limits over a rolling window, the hard signal from a refused credential, deferral when nothing is free, and cost attribution per credential ([`04-identity-secrets.md`](04-identity-secrets.md)).

Not built: the engine/runtime split as separate objects — an agent still names its engine directly and its LLM credential is still found by convention over fixed secret names, which is the constraint the rest of this document removes. Nor: the merit order, reported utilisation, the fixed/variable cost split, and capacity as a dispatch input.

Decided: a runtime is **org-scoped** (see above). That was the one question here whose answer is a data question rather than a policy one; everything below can be retrofitted.

Open points, carried in [`07-open-decisions.md`](07-open-decisions.md):

- **What is the scope of a reported utilisation figure** per engine — account-wide, or only the machine that asked? For ephemeral sandboxes the difference decides whether the figure is usable at all.
- **How is a fixed cost entered and spread?** A monthly amount per runtime is the simple answer; whether that is enough for organisations with mid-period changes to their seat count is not settled.
