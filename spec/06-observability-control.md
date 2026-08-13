# 06 — Guard rails, observability & control

This is the **trust layer** — the part that ultimately decides adoption. The aspiration: not logging, but **EDR/SIEM for agents**. An IT admin must be able to define centrally what agents *must not* do (guard rails), to see what they do (recording), to approve risky actions (gates) and to stop them in an emergency (kill switch).

Three points of effect that work together:

- **preventive** — guard rails stop forbidden actions before they happen,
- **interactive** — approval gates halt risky actions and fetch an approval,
- **retrospective** — session recording makes everything traceable, supervisor + kill switch react.

## Guard rails (central, platform-enforced)

The core: **limits are not left to the agent.** What sits in `SOUL.md` under "Limits" is self-binding via the prompt — valuable for steering behaviour, but **not a security boundary**, because a prompt can be worked around or defeated by injection (see the threat model in [`04-identity-secrets.md`](04-identity-secrets.md)). The *hard* limits are **guard rails**: defined centrally, versioned and **enforced outside the runtime**, at the places where the platform sits in the data flow anyway. A compromised agent cannot circumvent them, because they do not live in its reasoning.

### Enforcement points

Guard rails take effect exactly where the control plane and the daemon control the flow:

| Point | What is enforced here |
|---|---|
| **Secrets broker** | Which systems and scopes an agent can get a token for at all (see [`04-identity-secrets.md`](04-identity-secrets.md)). |
| **Egress** | Outbound communication: permitted recipients/domains, blocklists, mandatory approval for external addressees. |
| **Tool/action layer** (in the daemon) | Which tools and commands are allowed; destructive operations; file access outside the home. |
| **Approval queue** | Actions that are not forbidden but require approval (see below). |
| **Rate & cost limits** | Frequency of actions, budget caps (see cost control). |
| **Content filter** | Incoming/outgoing content: PII redaction, forbidden content classes. |

### Types of guard rail

- **Allow/deny for systems & tools** — e.g. "must never access the HR system", "no shell access".
- **Egress rules** — e.g. "mail only to internal domains without approval", "no outbound requests to unknown hosts".
- **Forbidden actions** — hard no-gos that are impossible even with approval.
- **Mandatory approvals** — actions that only run with a human sign-off.
- **Rate/cost limits** — upper bounds per time window / per task / per agent.
- **Content/PII rules** — redaction, classification, blocking of sensitive content.

### Scope & inheritance

Guard rails are **managed centrally** and take effect on three overlapping levels:

1. **Global** — apply to *all* agents (e.g. "no agent mails external addresses without approval"). Not overridable by agent config.
2. **Role / team** — apply to a class of agents (e.g. all support agents).
3. **Per agent** — additional, narrower rules for an individual agent.

Rules are **additive-restrictive**: a narrower level can tighten but never soften a global deny rule. The default is **fail-closed** — what is not allowed is forbidden; in doubt it is blocked or gated.

### Administration

Guard rails are themselves **config as code**: central, versioned, changed by review (analogous to the agent config in [`02-agent-model.md`](02-agent-model.md)). They are administered by the **security/compliance** role — deliberately separate from the agent owners, so that a single team lead cannot soften the org-wide limits (see roles & RBAC in [`09-enterprise-model.md`](09-enterprise-model.md)). Every guard-rail trigger (blocked/gated) flows into the recording and can raise alerts. So they are not only protection but also signal: frequently tripping rails point to a misconfigured — or compromised — agent.

Two tools make administration practical:

- **Pause instead of delete** — a rule can be deactivated without losing it. That keeps the rule history intact and makes experiments reversible; a paused rule does not take effect but stays visible and reactivatable.
- **Rule tester (dry run)** — a subject (a system or `system:action`, optionally in an agent's context) is evaluated dry against the current rules: the result is the decision (allow / deny / require_approval) including the triggering rule and the applicable budget cap. This lets a policy be verified *before* an agent runs into it — nothing is executed.

## Session recording

Complete recording of every agent activity, fed from the daemon's `event` messages (see [`01-architecture.md`](01-architecture.md)):

- **every LLM call** (prompt, answer, model, cost),
- **every tool call** (which tool, which parameters, which result),
- **every command in the sandbox** (shell, file operations),
- **every credential request** (which system, which scope, granted/refused),
- **every inter-agent message**.

Recording is the basis for audit, debugging, cost analysis and the supervisor evaluation. It is immutable and navigable in time per agent/task.

**Sub-runs stay distinguishable.** When an agent hands work to a sub-agent in the project checkout ([`12-claude-code-adapter.md`](12-claude-code-adapter.md)), that work lands under the same agent and task ID in the same recording — otherwise it would be neither billable nor auditable. So that you can nevertheless see **who** did what, those lines carry a marker with an identifier for the run, and the timeline collapses everything under one identifier into a single folded block: a header with the checkout, the assignment, the status and key figures (tool calls, turns, duration, cost), and the sub-agent's turns when expanded. Without that bracket its work would stand indistinguishably among the commissioning agent's own.

## Request log (diagnosing the target-system connection)

The recording says **what** an agent did — which action with which parameters and whether it worked. When connecting a target system the more pressing question, however, is **what went over the wire**: the bot connector call to Teams including its answer, the incoming webhook that failed the signature check. That is exactly what the **request log** captures (table `request_log`, view *Platform → Requests*):

- **outbound** — every request from a target-system plugin. The plugins build their HTTP client through `reqlog.Client(...)`; whether and where anything is logged is decided by a sink, not by the plugin. From the sandbox the entry travels as `event(kind=http)` over the daemon protocol and gets its org, agent and task reference in the control plane; requests the control plane makes itself (work checks for the heartbeat condition `nur-wenn:`, JWKS fetches) run through the default sink.
- **inbound** — every webhook and every generic trigger, **including the rejected ones**. A webhook that fails on the signature, the slug or a target system that is not enabled otherwise leaves no trace — and it is the most common failure during setup.

Distinction from the recording, deliberately as a separate table: the request log is **diagnostics, not an audit trail**. It has its own short retention window (`COVEY_REQUEST_LOG_RETENTION`, default 72 h, plus a hard row cap), it does not bloat the agent timeline, and it can be switched off entirely (`COVEY_REQUEST_LOG=false`). Writing happens asynchronously through a buffered channel — a request path never waits on diagnostics; if the buffer fills up, entries are dropped and counted.

Credentials do not belong in it: headers are not stored at all (that is where the bearer sits), suspicious query parameters and body fields (`token`, `secret`, `password`, `client_secret`, …) are replaced, bodies truncated at 8 KiB. Whoever also wants to keep the payloads (chat messages, ticket texts) out of the diagnostics table sets `COVEY_REQUEST_LOG_BODIES=false` — then only metadata remains. The view is reserved for the roles `org_admin` and `security`.

## Approval gates

Approval gates are the **interactive** guard-rail type: risky actions do not go through but wait for **approval**. The daemon reports `request_approval`; the control plane halts the action until a human (or a policy) delivers `approve`/`deny`.

Typical actions requiring a gate:

- outbound external mail (above all to non-internal recipients),
- deletion / destructive operations,
- bulk operations,
- access to particularly sensitive systems/scopes,
- anything a policy marks as risky.

Gates are configurable per agent and per action type. The default is conservative — when in doubt, gate.

## Supervisor agent (optional, AI-assisted)

An optional agent that **reviews other agents' activity and flags anomalies** — the AI-assisted monitoring component that was also asked for from the IT admin's perspective. It sees the recording streams and responds to:

- unusual access patterns (atypical systems, scopes, frequency),
- suspicious recipients of outbound communication,
- behaviour suggesting prompt injection (see the threat model in [`04-identity-secrets.md`](04-identity-secrets.md)),
- deviations from the mission defined in `SOUL.md`/`CAPABILITIES.md`.

The supervisor does not autonomously decide on hard interventions; it flags and escalates to the human or triggers approval gates.

**Not to be confused with Covey Doctor** ([`21-operations-and-improvement.md`](21-operations-and-improvement.md)), which also reads other agents' work. The two differ in what they are looking for and therefore in what they are allowed to see: the supervisor watches for **danger** — injection, unusual access, exfiltration — and needs the raw stream to spot it; Covey Doctor asks whether the work is **working** and gets facts the control plane recorded, with a raw recording only through an approval. Merging them would give one agent the widest read access on the platform *and* the ability to propose configurations, which is the combination both designs are built to avoid.

## Kill switch

An agent out of control? **Stop it immediately** — `pause`/`kill` to the daemon (see [`01-architecture.md`](01-architecture.md)). Two granularities:

- **individual** — a particular agent is stopped,
- **fleet-wide** — an emergency stop for all agents (e.g. on a suspected systemic injection attack).

## Cost control

Not optional but a prerequisite for the always-on economics (see [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)):

- **Cost tracking per agent** — from the `cost` events (tokens/compute per LLM call); aggregated up to department/cost-centre level for controlling (see [`09-enterprise-model.md`](09-enterprise-model.md)).
  **The input side is counted in three parts**: fresh input, a read out of the prompt cache and writing the cache. Not a detail — with a runtime that caches its context, the uncached share is the smallest of the three by orders of magnitude. Counting `input_tokens` alone gave one agent 5,497 input tokens against 1,842,222 output tokens, while a single one of its runs read 2.34 million cached tokens. The three are also priced differently (a cache read about a tenth of fresh input, writing it about a quarter more), so they are stored apart and only added up for display.
- **Attributed to the credential the run went out on**, not only to agent and task. An organisation holds several LLM contracts; without knowing which one paid for a run there is no per-credential limit, no utilisation, and no answer to whether a seat is too few or too many. See [`18-runtimes-capacity.md`](18-runtimes-capacity.md).
- **The figure is a list-price equivalent, and is labelled as one.** The runtime computes it from token counts at standard list prices. On a metered credential (an API key) that is what was billed; on a subscription seat it is what the same work *would* have cost through the API — the seat is paid for regardless. Deliberately no second cost model beside it: one figure, booked unchanged for every credential. It measures consumption comparably across both, it overstates spend on seats, and it overstates it in the harmless direction (agents look more expensive, not less). For an authoritative statement about money the provider's own billing view is the record. See [`18-runtimes-capacity.md`](18-runtimes-capacity.md).
- **Budget per agent** — a configurable cap; on exceeding it the agent is paused (kill switch, the running task goes back into the backlog). It is measured against the agent's **cumulative** cost, so it is a lifetime ceiling and not a per-run or per-period allowance: once passed, the agent stays paused until somebody raises the number. A cap per run or per time window does not exist yet.
- **Idle is idle** — the cheap dispatch loop consumes (almost) nothing; the expensive runtime only runs in `working`. Sandboxes hibernate in `sleeping`/`blocked`.

Without these three mechanisms the bill scales away from you at the tenth agent.

## The admin view

The platform views are **role-scoped** (see RBAC in [`09-enterprise-model.md`](09-enterprise-model.md)): an agent owner sees their agents, security/compliance the guard rails, the auditor the audit trail read-only, controlling the costs. In the respective view the human gets:

- **live status** of all agents (who is asleep, who is working, who is blocked),
- **backlog insight** (what sits in whose list),
- a **recording timeline** per agent/task,
- the **request log** of the target-system connection (Platform → Requests),
- **alerts** from the supervisor and from triggered guard rails,
- a **cost dashboard** per agent and aggregated,
- **guard-rail administration** (global / role / agent, versioned),
- **controls**: approval queue, kill switch, budget settings.

This view is what turns a collection of agents into a *manageable organisation*.
