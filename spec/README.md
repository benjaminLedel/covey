# Covey — Specification

> **Codename: Covey.** A *covey* is a small, coordinated flock — a group that moves together. That is exactly what this platform is: many agents, centrally orchestrated.

A central platform that treats AI agents like employees — with an identity, a workplace, credentials, a backlog and a manager — and gives the IT admin the tooling to lead and supervise them.

**Covey's unit is the organisation, not the individual user.** That is the load-bearing distinction from single-user "AI employee" apps: Covey is the platform a *company* operates to manage and govern its entire agent workforce — with many human stakeholders (IT, team leads, security/compliance, audit, controlling), central governance and a company-wide org chart. Agents are organisation-owned resources, not personal assistants. Details in [`09-enterprise-model.md`](09-enterprise-model.md).

The guiding metaphor from which the whole architecture follows: the platform is the **IT and HR department for AI agents**. Nearly every component has a counterpart in a real company, and that is exactly what gives us the blueprint — proven prior art can be adopted everywhere instead of reinvented.

| In a company | On the platform |
|---|---|
| Identity / Active Directory | Agent identity (email optional) |
| Workplace / PC | Isolated, persistent sandbox |
| Onboarding / org chart | `SOUL.md` + org structure |
| Workforce & departments | Org-owned agents, teams, cost centres |
| HR / IT administration | Human roles + RBAC + SSO |
| Password vault / PAM | Secrets broker (short-lived tokens) |
| Task list / ticket | Backlog (first-class object) |
| Operations manual / compliance | Central guard rails (platform-enforced) |
| SIEM / EDR | Session recording + alerts + kill switch |

## Documents

| File | Contents |
|---|---|
| [`01-architecture.md`](01-architecture.md) | System overview, control plane vs. data plane, runtime abstraction, daemon protocol |
| [`02-agent-model.md`](02-agent-model.md) | The agent as an entity: identity, sandbox, credentials, config as code, org chart |
| [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md) | State machine, dispatch loop, wake sources, backlog, blocking, event correlation |
| [`04-identity-secrets.md`](04-identity-secrets.md) | Keycloak, RFC 8693 token exchange, secrets broker, threat model |
| [`05-memory.md`](05-memory.md) | Memory layers, LLM wiki (linked Markdown pages + pgvector index), persistent home |
| [`06-observability-control.md`](06-observability-control.md) | Central guard rails, session recording, approval gates, kill switch, cost control, supervisor agent |
| [`07-open-decisions.md`](07-open-decisions.md) | Open questions, build vs. buy, MVP scope |
| [`08-market.md`](08-market.md) | Market research: competing platforms, open-source building blocks, build-vs-adopt matrix |
| [`09-enterprise-model.md`](09-enterprise-model.md) | The organisation as the unit: human roles & RBAC, SSO, tenants, cost centres, compliance |
| [`10-architecture-stack.md`](10-architecture-stack.md) | Frontend, backend language (Go/Kotlin), "batteries included, but swappable", pluggable interfaces, the Postgres anchor |
| [`11-mvp-plan.md`](11-mvp-plan.md) | Build order: milestones M0–M7, critical path, acceptance checklist |
| [`12-claude-code-adapter.md`](12-claude-code-adapter.md) | First runtime adapter: driving Claude Code headless via `claude -p`, flag mapping, `blocked`↔`--resume` |
| [`13-zammad-integration.md`](13-zammad-integration.md) | MVP target system Zammad: wake via trigger/webhook, REST actions, broker token, `blocked`↔`pending`, correlation via ticket ID |
| [`14-companion-memory.md`](14-companion-memory.md) | Companion app: universal brain dump (audio/mail/screen/documents) → curated wiki with media → context for agents; memory curator, bearer auth, data protection |
| [`15-teams-integration.md`](15-teams-integration.md) | Target system Microsoft Teams (Azure Bot Framework): wake via messaging endpoint (JWT-verified), bot connector actions, OAuth2 broker, `blocked`↔conversation, correlation via `conversation.id` |
| [`16-runner.md`](16-runner.md) | Distributed data plane: registered runners modelled on GitLab, runner protocol, central home store (content-addressed, deduplicated), per-agent sandbox images, trust boundary |
| [`17-kpis.md`](17-kpis.md) | Performance indicators: KPIs as counting rules over recorded evidence, `KPIS.md` per agent, unit cost and productive share against the cost figures |
| [`18-runtimes-capacity.md`](18-runtimes-capacity.md) | Runtimes as contracts: engine vs. configured workplace, credential pools per provider, merit order, reported utilisation, fixed vs. variable cost |

## Design principles

1. **The organisation is the unit, not the user.** Covey is an enterprise platform: org-owned agents, several human roles with RBAC, central governance, a company-wide org chart. Not a single-user productivity tool.
2. **The control plane is the product.** Sandboxes are commodity, runtimes are swappable. The value sits in scheduling, identity, governance and observability — the layer nobody else builds.
3. **Runtime-agnostic.** The platform manages the sandbox, not the framework. A slim daemon with a uniform protocol runs inside the sandbox; OpenHands, Harness, Claude Code & co. are interchangeable behind it.
4. **Always reachable, compute only on demand.** "Always-on" is a UX property, not a runtime property. Idle has to mean idle, or the bill scales away from you.
5. **Config as code.** Agent behaviour is versioned in Git. Changes go through PR/review, not through a deploy. Audit falls out for free.
6. **Never put long-lived secrets in the sandbox.** Access is brokered at runtime, short-lived and scoped.
7. **Guard rails central and platform-enforced.** Hard limits are not left to the agent (a prompt can be worked around or injected into) but defined centrally and enforced outside the runtime — at the broker, at the egress, in the tool layer. Fail-closed.
8. **Trust by design.** Without complete traceability, approvals and a kill switch there is no adoption. Observability is not an add-on, it is a prerequisite.
9. **Serial before parallel.** An agent works on one task at a time. Parallelism is a question of more agents, not of concurrency inside one agent.
10. **Batteries included, but swappable.** Every platform capability (IdM, secrets, queue, observability) has a simple DB-backed built-in default and a narrow interface for an external provider. The MVP runs with `builtin` everywhere — binary + Postgres + sandbox infra; Keycloak/Vault/Redis/Langfuse can be switched on optionally, they are never prerequisites.

## Glossary

- **Agent** — A configured, persistent entity with an identity, a sandbox, credentials and a backlog. The counterpart to the employee. An email address of its own is optional, not mandatory.
- **Guard rail** — A centrally defined, platform-enforced limit on agent behaviour (e.g. an egress rule, a forbidden system/tool, a mandatory approval). It takes effect outside the runtime and cannot be circumvented by the agent.
- **Engine** — The agent framework that runs the actual LLM loop (OpenHands, Harness, Claude Code …). A self-registering plugin, shipped with the binary, swappable. There is one of each.
- **Runtime** — A configured workplace: an engine plus the capacity to run it — credential(s), model, limits. Data, not code; it has a name a person chose ("Claude subscription Ben"), and an agent is assigned to it. See [`18-runtimes-capacity.md`](18-runtimes-capacity.md).
- **Capacity** — What a runtime can do before it runs out. Two kinds that pull in opposite directions: **quota** (a subscription seat, paid for regardless — unused quota is wasted money) and **metered** (an API key, billed per use — every token costs).
- **Daemon** — The slim process inside the sandbox that speaks the uniform platform protocol and bootstraps the runtime.
- **Control plane** — The central service: scheduler, identity broker, backlog store, observability. Knows the state of every agent.
- **Data plane** — The totality of sandboxes in which agents actually work.
- **Dispatch loop** — The cheap, permanently running orchestration loop per agent (no LLM) that processes wake events.
- **Tick** — A periodic "anything to do?" impulse that makes an agent proactive.
- **Backlog** — An agent's persistent, prioritised task list.
- **Secrets broker** — The service that issues agents short-lived, scoped access tokens for target systems.
- **Supervisor agent** — An optional agent that reviews other agents' activity and flags anomalies.
- **Enforcement point** — A place where the platform sits in the data flow anyway (broker, egress, tool layer) and where guard rails are enforced technically.
- **Organisation / tenant** — The unit a Covey instance is operated for. All agents, roles, guard rails, budgets and audits are org-scoped.
- **Human role** — A person with defined rights on the platform (e.g. platform admin, agent owner, security/compliance, auditor, controlling). Governed by RBAC, authenticated via SSO.
- **Agent owner** — The person (usually a department's team lead) accountable for a particular agent: its config, its backlog priority, its approvals.
- **Companion** — The dedicated (mobile/desktop) app for offloading the entire brain load (audio, mail, screen recording, documents, links) in one place. The memory curator condenses it into a wiki with media (linked pages + `pgvector`); private by default, shareable with your own agents on request — as their context. See [`14-companion-memory.md`](14-companion-memory.md).
- **Memory curator** — An org-owned agent that cuts a person's raw brain dump (captures) into linked wiki pages — config as code instead of a hardcoded LLM call. See [`14-companion-memory.md`](14-companion-memory.md).
