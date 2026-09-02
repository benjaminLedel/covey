# 13 — Zammad integration (the MVP target system)

Makes the "one target system" from M5 in [`11-mvp-plan.md`](11-mvp-plan.md) concrete as **Zammad** (an open-source helpdesk, self-hostable, REST/JSON API, triggers + webhooks). Zammad touches three MVP milestones: the event wake (M3), event correlation (M4) and broker + API actions (M5).

Architecturally Zammad is the **first compiled target-system plugin** (`github.com/benjaminLedel/covey-plugin-pack/zammad`). It does not live in covey's repository: like every plugin it is an ordinary Go package against the public SDK, in the plugin pack, pulled in by blank import — see [`10-architecture-stack.md`](10-architecture-stack.md), "Target systems as plugins". covey can be built lean without Zammad, and further target systems arrive as further plugins, as uploaded JSON manifests or as WebAssembly modules without touching the core.

It fits well because Zammad is widespread in German-speaking countries and, like covey, self-hostable — both run on the same infrastructure.

## Three integration surfaces

### 1. Inbound: wake via triggers + webhooks (M3/M4)

Zammad knows **triggers** (they react to ticket lifecycle events: created, status changed, new message) and **webhooks** (a POST to an external URL). A trigger fires the webhook at covey's event router.

- **New ticket** → trigger → webhook → event router → the support agent wakes up (the M3 wake source).
- **Customer reply / ticket update** → trigger → webhook → correlation → the blocked agent wakes up (M4).
- The webhook payload is JSON and contains the ticket (including the **`id`** and `article_ids`), the article, the group and the user; its integrity is verifiable through an **HMAC-SHA1 signature** in the header (an optional signature token) — covey checks the signature before trusting the event.
- Operational reality: webhooks are **not guaranteed to arrive immediately** (the same priority/ordering as email triggers) and are retried up to four times on failure. The event router therefore has to be idempotent (the same ticket update must not trigger two wakes).

### 2. Outbound: actions through the REST API (M5)

Base `/api/v1/`, `Content-Type: application/json`. The actions the support agent needs in the MVP:

| Action | Call |
|---|---|
| Read the ticket | `GET /tickets/{id}` |
| Read the history (articles) | `GET /ticket_articles/by_ticket/{ticket_id}` |
| Write a reply/comment | `POST /ticket_articles` (or an `article` object in the ticket update) |
| Set status/owner/priority | `PUT /tickets/{id}` |

On articles, `internal: true|false` controls visibility (an internal note vs. visible to the customer) and `type` the kind (note, email, …). With that the agent covers triaging, replying, commenting internally and escalating (changing group/owner).

### 3. Auth: the broker against Zammad (M5)

Zammad supports three auth methods: HTTP basic, **token access** (permission-scoped API tokens) and OAuth2. For covey:

- **Token access** is the way: a **dedicated role** with exactly the necessary rights (e.g. `ticket.agent` for particular groups — least privilege), with the token created in the admin interface under *System → API* ("Token access allowed").
- The **secrets broker** holds this token and injects it into the daemon at runtime — **nothing long-lived in the sandbox** (see [`04-identity-secrets.md`](04-identity-secrets.md)).

> **An honest limit.** Zammad's API token is a permission-scoped but **long-lived** token — **not** a short-lived one exchanged per RFC 8693. Zammad therefore falls into the "target system connected simply by API key" case from [`10-architecture-stack.md`](10-architecture-stack.md): the built-in `SecretStore` keeps the Zammad token encrypted and passes it through short-lived; real token exchange only applies to OAuth-capable target systems. For the MVP exactly that is sufficient and honestly delineated.

## `blocked` ↔ Zammad's `pending` state (M4)

Zammad has a **`pending reminder`/`pending close`** state with a `pending_time` — that maps covey's `blocked` natively and at the same time keeps the **human view consistent**: the ticket visibly sits at "waiting for the customer", neither open nor closed.

Sequence:

1. The agent asks the customer a follow-up question (`POST /ticket_articles`, `internal: false`) and sets the ticket to `pending reminder` (`PUT /tickets/{id}`).
2. covey parks the task → `blocked`, **correlation key = the Zammad ticket `id`**, plus the Claude Code `session_id` (see [`12-claude-code-adapter.md`](12-claude-code-adapter.md)).
3. The customer replies → a Zammad trigger fires the webhook (ticket update, `sender: Customer`).
4. covey correlates via the ticket `id`, wakes the agent and continues via `claude -p --resume <session_id>`.

## Correlation — practically free for Zammad

The open decision D1 (event correlation, see [`07-open-decisions.md`](07-open-decisions.md)) is simple for Zammad: the **ticket `id` is a stable, natural correlation key** that comes along in every webhook payload. No key of its own has to be looped back through the customer communication — the **central event router** simply maps `ticket.id` → parked task. That is the pragmatic starting point; the general, channel-independent mechanism stays relevant for later target systems.

## MVP scope of this integration

- **One group / one support agent**, token auth through the built-in `SecretStore`.
- **Trigger + webhook** on "ticket created" and "customer reply", HMAC-verified, processed idempotently.
- **Actions:** read, reply (internal/external), set status/owner.
- **`blocked`** through Zammad's `pending` state, correlation through the ticket `id`.

Later (not MVP): OAuth2 instead of a token, several groups/agents, attachments (the webhook only delivers links, auth needed), further target systems through the same broker interface.

## Notes

- Zammad webhooks have existed since 3.6, freely customisable payloads since 6.0. Checked against the Zammad documentation (`docs.zammad.org`, `admin-docs.zammad.org`, as of July 2026) — check briefly before building.
- Both systems (covey and Zammad) run self-hosted; the webhook path Zammad → covey and the API path covey → Zammad stay internal, with no detour through third parties.
