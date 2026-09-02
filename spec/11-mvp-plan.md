# 11 — MVP plan

Translates the MVP scope from [`07-open-decisions.md`](07-open-decisions.md), the BUILD rows of the matrix from [`08-market.md`](08-market.md) and the stack from [`10-architecture-stack.md`](10-architecture-stack.md) into a concrete build order.

## Goal (definition of done)

A **support agent** that triages a ticket, answers it itself or escalates it, goes cleanly `blocked` on a follow-up question, wakes up correctly again on the incoming answer and writes the solution into memory — **fully recorded, fenced in by central guard rails and with a kill switch**. When this one vertical slice runs, covey's core stands.

## MVP principles

- **The thinnest vertical slice first.** Not layer by layer horizontally, but an end-to-end runnable (if trivial) chain early.
- **`builtin` everywhere.** No external heavyweights in the MVP (identity/secrets/queue/observability as built-ins, see [`10-architecture-stack.md`](10-architecture-stack.md)).
- **Exactly one of everything.** One agent type (support), one runtime (Claude Code via ACP), one target system (the ticket system), serial.
- **De-risk the `blocked` loop early.** It is both the defining *and* the riskiest part — design before build (settle D1).
- **Every milestone is demonstrable.** No milestone without a visible result in the UI or in the log.

## Milestone overview

| # | Focus | Size | Risk | Core dependency |
|---|---|---|---|---|
| **M0** | Walking skeleton (binary + Postgres + UI shell + migrations) | M | low | — |
| **M1** | Sandbox + daemon protocol + one runtime | L | **high** | M0 |
| **M2** | Config as code (`SOUL.md` → prompt) | S | low | M1 |
| **M3** | Backlog + state machine (serial, without `blocked`) | M | medium | M2 |
| **M4** | **The `blocked` loop + event correlation** | M | **high** | M3, D1 |
| **M5** | Secrets broker + one target system (ticket system) | M | medium | M2 |
| **M6** | Guard rails + recording + kill switch + cost + RBAC | L | medium | M3 |
| **M7** | Memory (pgvector: query@triage, ingest@done) | S | low | M3 |

The order is not strictly linear: **M5** depends on M2 (not on M4) and can run in parallel with M3/M4; **M6** and **M7** build on M3.

---

## M0 — Walking skeleton

**Goal:** the deployable spine stands, still without anything agentic.

- **Build:** a Go binary with `serve`/`migrate`/`bootstrap`; a Postgres schema (agent registry, orgs, roles) via embedded migrations; an embedded React/Tailwind admin shell that lists/creates agents; config through ENV; `/healthz` + `/readyz`.
- **Adopt:** —
- **Result:** `covey migrate up && covey serve` runs; an agent can be created and appears in the UI. Proves the stack from [`10-architecture-stack.md`](10-architecture-stack.md) end to end.

## M1 — Sandbox + daemon protocol + one runtime

**Goal:** the data plane is alive; an agent can think inside a sandbox.

- **Build:** the bidirectional **daemon protocol** (`wake`/`assign_task`/`event`/`sleep`, see [`01-architecture.md`](01-architecture.md)) in minimal form; a slim daemon that runs in the sandbox; **one adapter** for Claude Code, concretely through the **headless mode `claude -p`** (details in [`12-claude-code-adapter.md`](12-claude-code-adapter.md)).
- **Adopt:** the sandbox infrastructure (E2B or Beam, see [`08-market.md`](08-market.md)); Claude Code as the runtime. (ACP as a generic multi-runtime standard only post-MVP, for further runtimes.)
- **Result:** "wake" from the UI → the sandbox starts → the daemon connects → assign a trivial task → output/events stream back. **The highest infrastructure risk — hence early.**

## M2 — Config as code

**Goal:** the agent's behaviour comes from its configuration.

- **Build:** `SOUL.md` + a minimal MD set in DB/Git; compilation into a system prompt + runtime config; injection via `inject_config` on wake (see [`02-agent-model.md`](02-agent-model.md)).
- **Result:** change `SOUL.md` → changed behaviour at the next wake. Versioned, by review.

## M3 — Backlog + state machine (serial)

**Goal:** the agent has a real working life — apart from blocking.

- **Build:** the backlog as a first-class Postgres object (state/priority/origin/history); the state machine `sleeping → triggered → triage → working → done`; the **dispatch loop** (Postgres `SKIP LOCKED` + `LISTEN/NOTIFY`); the wake sources event (a new ticket via a **Zammad trigger → webhook**, see [`13-zammad-integration.md`](13-zammad-integration.md)) + manual; strictly serial (see [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)).
- **Result:** put a task into the backlog → the agent wakes, works it serially, marks it `done`, sleeps. Live status in the UI.

## M4 — The `blocked` loop + event correlation

**Goal:** the agent becomes an employee — it parks and wakes up correctly again. **The heart of the MVP.**

- **Prerequisite:** decision **D1** (correlation key vs. central event router vs. hybrid) — *to be settled as a design spike before building*, see [`07-open-decisions.md`](07-open-decisions.md).
- **Build:** the `blocked` state with suspension; a correlation key on parking; **one** wake channel (ticket update). For Zammad the correlation key is the ticket **`id`** (it comes along in the webhook) and `blocked` maps onto Zammad's **`pending` state** — see [`13-zammad-integration.md`](13-zammad-integration.md). Resumption uses Claude Code's native **`--resume <session_id>`** mechanic (the `session_id` is stored on the parked task) — see [`12-claude-code-adapter.md`](12-claude-code-adapter.md).
- **Result:** the agent asks a follow-up question, goes `blocked`, the sandbox shuts down; the incoming answer correlates and wakes it to continue. No polling, no hallucinating.

## M5 — Secrets broker + one target system

**Goal:** the agent acts in a real system — without secrets in the sandbox.

- **Build:** `IdentityProvider` + `SecretStore` in the **built-in variant** (signed JWTs, AES-GCM secrets in Postgres, see [`10-architecture-stack.md`](10-architecture-stack.md)); the broker keeps the **Zammad API token** (a permission-scoped role) and injects it short-lived; a least-privilege connection to **Zammad** (read tickets / reply / set status), see [`13-zammad-integration.md`](13-zammad-integration.md).
- **Adopt:** **Zammad** as the target system (self-hosted, REST API + webhooks); optionally Keycloak, if available (otherwise `builtin`).
- **Result:** the agent reads/writes real Zammad tickets through brokered credentials — nothing long-lived in the sandbox (see [`04-identity-secrets.md`](04-identity-secrets.md)).

## M6 — Guard rails + recording + kill switch + cost

**Goal:** the trust layer — without which there is no adoption.

- **Build:** a minimal **central guard-rail set** (egress deny without approval, deny for non-approved systems/tools, mandatory approval for destructive actions, fail-closed); session recording in Postgres; the kill switch (individual + fleet-wide); simple cost tracking per agent; **role-scoped views** (basic RBAC, see [`06-observability-control.md`](06-observability-control.md), [`09-enterprise-model.md`](09-enterprise-model.md)).
- **Result:** everything the agent does is recorded and inspectable; risky actions are gated; the agent can be stopped immediately; cost per agent is visible.

## M7 — Memory

**Goal:** the agent knows the shop.

- **Build:** built-in memory over **pgvector**; query in the `triage` step, ingest in the `done` step (see [`05-memory.md`](05-memory.md)). MVP baseline: a flat snippet store; evolution into the **wiki** (linked Markdown pages + pgvector index, consolidation pass) through the same seam.
- **Adopt:** Graphiti only post-MVP, if real temporal reasoning beyond the wiki is needed.
- **Result:** "I dealt with this customer last week, the solution was Y."

---

## Critical path

Two milestones carry the risk: **M1** (sandbox/daemon/runtime — infrastructure) and **M4** (`blocked` + correlation — the defining mechanic). Tackle both early; M1 as the first technical risk, M4 with a design spike (D1) *before* building. The rest (M0, M2, M3, M5, M6, M7) is comparatively standard engineering.

## Explicitly later (not MVP)

Further runtimes/adapters · the supervisor agent (AI anomaly detection) · a shared org-wide memory · inter-agent communication via A2A/MCP · a fully built-out admin dashboard · externalisation onto Keycloak/Vault/Redis/Langfuse/Graphiti · multi-tenancy (D9). All of them arrive through the interfaces already drawn, without changing the core.

## Acceptance checklist (the one vertical slice)

A new **Zammad** ticket arrives and the support agent demonstrably runs through:

- [ ] **Wake** by the Zammad webhook (a trigger on "ticket created", not by polling).
- [ ] **Triage:** backlog + memory checked, prioritised.
- [ ] **Working:** access to Zammad through the brokered API token (no secret in the sandbox).
- [ ] **Blocked:** a follow-up question put to the customer, the ticket set to `pending`, gone cleanly `blocked`, the sandbox shut down.
- [ ] **Wake on correlation:** the incoming customer reply correlates via the ticket `id`, the agent continues via `--resume`.
- [ ] **Done:** solved or escalated; the solution written into memory.
- [ ] **Guard rails:** a risky action (e.g. external mail) is gated.
- [ ] **Recording:** the entire session is completely recorded and inspectable.
- [ ] **Kill switch:** the agent can be stopped immediately at any time.
- [ ] **Cost:** tokens/compute consumed per agent are visible.

If all the points are green, the MVP guiding question is answered.
