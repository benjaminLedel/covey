---
slug: zammad
title: Zammad
description: 'From the demo setup to a real Zammad instance: installing the catalogue plugin, the webhook, intake groups and how an agent answers a ticket.'
---

A practical runbook for the step from the demo setup (`demo/fakezammad`) to a
**real Zammad instance**. The design background is in
[`../spec/13-zammad-integration.md`](../../../spec/13-zammad-integration.md); this
document says **what concretely has to be done** — and where the limits for
production use lie.

> Short version: the adapter is already Zammad-compatible (token auth,
> HMAC-SHA1 webhook, `/api/v1` paths). These are essentially
> **configuration steps**, not a rebuild. Two behavioural points
> (customer-visible replies, ticket selection) you should set deliberately.
>
> **Since plugin 1.1.0 the webhook is optional.** An agent finds the tickets
> assigned to it by itself (`list_tickets`), and a heartbeat with
> `nur-wenn: zammad:assigned` wakes it only when one of them changed — see
> section 2.4a. The webhook remains the way to a wake *the moment* a customer
> writes, and the way a parked (`blocked`) question is resumed.

---

## 1. Overview of the data flow

```
covey   ──(heartbeat pre-check, REST, token auth)──►  Zammad   "which of my tickets are new or open?"
   │  the answer changed → wake                         (nur-wenn: zammad:assigned)
   ▼
 agent (sandbox, Claude Code)
   │  actions through the action proxy
Zammad  ◄──(REST /api/v1, token auth)──────────────┘  list_tickets, get_ticket, list_articles,
                                                      reply, set_state, assign, escalate

Zammad  ──(optional: trigger + webhook, HMAC-signed)──►  covey  /api/webhooks/zammad/<agent-slug>
                                                            check signature → intake filter → task / resume
```

Two directions, two auth routes:

- **Outbound** (covey → Zammad): REST with a brokered API token
  (the secret `zammad_token`) that is never persisted in the sandbox. The
  heartbeat pre-check uses the same token from the control plane.
- **Inbound** (Zammad → covey), optional: a webhook, verified by an HMAC-SHA1
  signature (`COVEY_ZAMMAD_WEBHOOK_SECRET`).

---

## 2. Step-by-step instructions

> **Installing it.** As of 0.6.0 this is a **catalogue plugin**, not a compiled
> one — Store → Catalogue → Zammad → Install. covey verifies the digest the
> catalogue pins before storing the module. Upgrading across 0.6.0 with the
> plugin already in use: the plugin row and its secrets survive, only the code
> now arrives from the catalogue, so install it once afterwards and the agents
> keep their access. A newer catalogue version shows up in the same place as
> *Update to v…*; the secrets and the agents' access survive an update.

### 2.1 In Zammad: create an API token + rights

1. Create an **agent role** with least-privilege rights: `ticket.agent`
   for **exactly the group(s)** the agent should work on — no more.
2. Create a **user** (the "covey agent") with that role and assign them the
   target group.
3. Enable token access: *Admin → System → API →* switch on "Token Access".
4. As that user create an **API token** (*Profile → Token Access*) with the
   permissions `ticket.agent`. Note the token down (it is shown only once).

### 2.2 In covey: deposit the secrets

Set per agent in the SecretStore (UI: agent page → Secrets, or via the API):

| Secret | Value | Purpose |
|---|---|---|
| `zammad_url` | `https://helpdesk.example.com` | **without** `/api/v1` — the client appends that |
| `zammad_token` | the API token from 2.1 | outbound auth |
| `anthropic_api_key` *or* `claude_code_oauth_token` | an API key or `claude setup-token` | the runtime in the sandbox |

Without one of the Claude values, tasks fail with "Not logged in" — the sandbox
has its own empty `HOME`.

### 2.3 In covey: enable the target system

The target system `zammad` has to be **enabled** for the org (UI: Target
systems → enable Zammad). If it is not active, the broker refuses every
credential release fail-closed and the webhook endpoint rejects the event.

In addition the agent has to be allowed to access `zammad` according to its
`ACCESS.md`, and the guard rails must not forbid `zammad` /
`zammad:reply_external`.

### 2.4a Let the agent take up work by itself (no webhook)

The agent's heartbeat does the asking:

```
- alle: 10m nur-wenn: zammad:assigned titel: Work the queue aufgabe: list_tickets, read each ticket's articles, answer or hand over, end with done.
```

Before every interval the control plane reads, with the agent's token, the
tickets assigned to the agent's Zammad user that are in a state of type
*new* or *open* (`POST /tickets/search` with a selector — SQL on the Zammad
side, no Elasticsearch needed). The agent is woken only when that set changed:
a new ticket, a customer reply (it moves `updated_at`), a ticket set back to
open. Pending, closed and merged states are not work, so an agent that answers
and sets `pending reminder` is not woken by its own reply.

Three kinds:

| `nur-wenn:` | looks at |
|---|---|
| `zammad` / `zammad:assigned` | the tickets assigned to the token's own user |
| `zammad:unassigned` | open tickets nobody owns (the group's inbox) |
| `zammad:owner:<login or e-mail>` | that person's tickets **and** the agent's own |

The third form is for the setup where a person routes work to the agent by
assigning tickets to themselves: the agent works them, answers as itself and
leaves the ticket with its owner. If it takes one over (`assign` with
`owner:"me"`), the ticket stays in its view.

> The person named in `owner:` is matched **exactly** against login, e-mail or
> name. Zammad's own user search is a `LIKE`, and `ben` must not become
> `benno`. An unknown name is an error on the pre-check, which is visible on
> the agent's heartbeat row — not a silent fallback to the agent's own user.

Which tickets the agent may see at all is still Zammad's decision: the role
of the agent user (2.1) decides the groups, and `list_tickets` never shows a
ticket the token cannot read.

### 2.4 In covey: set the process env (webhook only)

```bash
COVEY_PUBLIC_URL=https://covey.example.com        # reachable from Zammad, NOT localhost
COVEY_ZAMMAD_WEBHOOK_SECRET=<long-random-secret>
```

> **Changed in 0.6.0.** `COVEY_ZAMMAD_INTAKE_GROUPS` and
> `COVEY_ZAMMAD_REPLY_TYPE` are gone. Zammad is a catalogue plugin now, and a
> WebAssembly module gets no process environment — see sections 3 and 4 for
> where each of them went.

`COVEY_PUBLIC_URL` has to be publicly (or, for Zammad, network-) resolvable,
otherwise Zammad cannot deliver the webhook.

### 2.5 In Zammad: set up the webhook + trigger (optional)

Skip this section if the heartbeat (2.4a) is enough. The webhook adds two
things: a wake within seconds of a customer article instead of at the next
interval, and the resume of a parked question (section 5).

1. Create a **webhook** (*Admin → Manage → Webhooks*):
   - Endpoint: `https://covey.example.com/api/webhooks/zammad/<agent-slug>`
     (`<agent-slug>` = the slug of the responsible agent in covey; the ticket is
     assigned to the agent through the URL — see section 3.2. The agent ID, the
     UUID from the agent page's URL, is accepted as an alternative.)

     > On an installation with **several organisations**, use the agent id. A
     > slug is unique only within its organisation, and covey refuses to deliver
     > an ambiguous one with a 404 rather than guessing which `support` was
     > meant.
   - HMAC signature token: **the same value** as `COVEY_ZAMMAD_WEBHOOK_SECRET`.
   - Payload: the standard payload is fine; it has to contain `ticket` and
     `article` as top-level objects. Important: `article.sender` has to arrive
     as a string ("Customer"/"Agent"), and for the group filter
     `ticket.group` (the group name) has to be included.
2. Create a **trigger** (*Admin → Manage → Trigger*):
   - Condition, e.g. "Action: ticket created/updated" **and**
     "Article sender: customer".
   - Action: "Trigger webhook" → the webhook from step 1.
   - Optionally add conditions such as "Group = Support L1" (see 3.1).

### 2.6 Testing

1. Create a ticket in the target group in Zammad (reply as the customer) and
   assign it to the agent's user.
2. In covey: does the heartbeat fire at the next interval (the heartbeat row
   shows the pre-check's answer), or — with the webhook — does a backlog task
   appear at once? → look at the recording.
3. If the agent replies, check: does the reply arrive **visibly for the
   customer** (section 4)?
4. On a follow-up question: does the ticket go to `pending reminder` and the
   agent to `blocked`? Customer reply → does the agent wake up again?
   (section 5)

---

## 3. Which tickets does the agent take up?

**By polling (2.4a):** the tickets assigned to the agent's user, or to the
person named in `nur-wenn: zammad:owner:<login>`, in a state of type *new* or
*open*. Assignment is the filter, and it sits where a team already does its
routing: in Zammad, by giving the ticket to a colleague. `list_tickets` lets
the agent look beyond that on purpose (`owner:"nobody"` for the group's
inbox, another login, `state:"any"`); the playbook says whether it should.

**By webhook:** that can be steered on **two levels** — both together yield
the intake decision.

### 3.1 Level 1 — Zammad side (trigger conditions)

The cleanest filter sits at the source: the Zammad **trigger** fires the
webhook only when its conditions are met (group, priority, state, tag, owner,
…). What the trigger does not let through never reaches covey at all.
For "only tickets of the group *Support L1*" a trigger condition suffices.

### 3.2 Level 2 — covey side (intake filter)

If a webhook reaches covey, the adapter decides whether a task comes out of it.
Two criteria, both of which must apply (`ShouldWake` in
`github.com/benjaminLedel/covey-plugin-pack/zammad/webhook.go`):

1. **A customer message:** `article.sender == "Customer"` and not internal. That
   way the agent's *own reply* does not trigger a new wake cycle.
2. **A group allowlist — set it in the Zammad trigger, not in covey.**

   `COVEY_ZAMMAD_INTAKE_GROUPS` is gone as of 0.6.0, and it is not replaced by a
   plugin setting. A Zammad trigger has had a condition on the group all along:
   add **Group is «Support L1»** to the trigger's conditions and only those
   tickets are delivered at all.

   That is the better place for it and always was. The env var applied the same
   filter one step too late — after Zammad had built and sent the request, and
   after covey had verified its signature, only to discard it.
   (backwards-compatible). Prerequisite: the webhook payload contains
   `ticket.group` as a name.

**Recommendation:** use level 1 (the trigger) as the primary filter — it saves
network round trips and keeps the selection with the department. Level 2 as a
safety net / central enforcement, in case the trigger is drawn too widely.

### 3.3 Mapping ticket → agent

Today the mapping runs **solely through the `<agent-slug>` in the webhook URL**.
For several support areas you create one Zammad webhook per agent (with a
matching trigger condition) on that agent's slug URL. A central queue→agent
mapping does not (yet) exist in covey — see section 7.

---

## 4. Customer-visible replies

The adapter distinguishes:

- **internal** (`reply` with `internal:true`) → a Zammad article of type `note`,
  visible only to agents.
- **external** (`reply` with `internal:false`) → type **`email`** (default), it
  goes out as mail to the customer.

> Important: an external *note* would be visible in the ticket but would
> trigger **no mail** — the failure where the agent believes it answered and the
> customer never heard. That is why the default for external replies is `email`.
> For a web or chat instance the agent names the type per answer:
>
> ```json
> {"ticket_id": 42, "body": "…", "internal": false, "reply_type": "web"}
> ```
>
> `COVEY_ZAMMAD_REPLY_TYPE` is gone as of 0.6.0. It was always a per-answer
> decision wearing the clothes of an installation-wide one.

---

## 5. `blocked` ↔ Zammad `pending`

**Without the webhook** a run ends with `done`, always. A follow-up question is
`reply` to the customer plus `set_state` to `pending reminder`; the customer's
answer moves the ticket back to *open* in Zammad, the pre-check sees the
change, and the next interval wakes the agent with the ticket in
`list_tickets` again. `blocked` would park a task that nothing resumes.

**With the webhook:** if the agent asks a follow-up question, it sets the
ticket to `pending reminder` (`set_state`) and goes `blocked` itself. The correlation key is the
ticket `id`. The customer's reply (a new customer article) fires the trigger,
covey correlates via the ticket `id` and continues the agent via
`claude -p --resume`. Details: [`../spec/13-zammad-integration.md`](../../../spec/13-zammad-integration.md).

Make sure the trigger fires **for ticket updates too** (not just "created") —
otherwise a blocked agent never wakes up again.

---

## 6. Env reference (Zammad-relevant)

| Variable | Default | Meaning |
|---|---|---|
| `COVEY_PUBLIC_URL` | `http://localhost:8494` | The base URL at which Zammad reaches the webhook |
| `COVEY_ZAMMAD_WEBHOOK_SECRET` | *(empty = signature check off)* | The HMAC-SHA1 secret, identical to the Zammad webhook token |
| `COVEY_DAEMON_TOKEN_TTL` | `15m` | The TTL of the credential passed into the sandbox |
| `COVEY_EGRESS_ENFORCE` | `true` | The egress allowlist proxy (only the `docker` provider); `false` switches it off |
| `COVEY_EGRESS_ALLOW` | *(empty)* | Additional permitted egress hosts, e.g. the Zammad host (`*.suffix` allowed) |
| `COVEY_EGRESS_ISOLATION` | `proxy` | `proxy` (cooperative) or `network` (hard isolation, see 6.1) |
| `COVEY_EGRESS_LISTEN_ADDR` | *(empty = a free port)* | The cooperative proxy's bind address inside the control plane; fixed (`:8495`) when covey runs in a container, with the same port published |
| `COVEY_EGRESS_PROXY_ADDR` | `:8888` | The proxy's bind address (network mode, in the container) |
| `COVEY_CONTROL_URL` | *(set by the control plane)* | The control plane's address as the proxy container sees it — set automatically, only relevant when running `covey egress-proxy` by hand |
| `COVEY_RUNNER_TOKEN` | *(set by the control plane)* | The token of the runner the proxy belongs to — likewise set automatically |

> **Egress:** with `COVEY_SANDBOX_PROVIDER=docker` the sandbox traffic runs
> through an allowlist proxy (the default; `COVEY_EGRESS_ENFORCE=false` switches
> it off). `api.anthropic.com` (the
> runtime) is permanently allowed; **you have to add the Zammad host**, otherwise
> the agent cannot reply. Two routes:
>
> The allowlist is **per agent**: effective = permanently allowed hosts +
> assigned templates + the agent's own hosts.
> - **In the interface** (recommended): *Egress* (side menu) maintains the
>   reusable **templates** and shows the global monitoring; the
>   **assignment per agent** (templates + own hosts) sits under
>   *Settings → Egress* on the agent page. It takes effect within ~15 s, with no
>   restart. Rights: `security` or `org_admin`.
> - **By ENV/Compose** (applies to ALL agents, not deletable in the UI):
>   ```bash
>   COVEY_SANDBOX_PROVIDER=docker
>   COVEY_EGRESS_ENFORCE=true
>   COVEY_EGRESS_ALLOW="helpdesk.example.com"
>   ```
>
> Patterns: an exact host or `*.suffix`. The standard mode is cooperative
> (HTTP(S)_PROXY in the container) — it prevents naive exfiltration but can be
> circumvented by the agent through direct IPs. For hard isolation see below.

### 6.1 Hard network isolation (`COVEY_EGRESS_ISOLATION=network`)

In network mode the sandbox runs on an **internal Docker network without
internet**; a proxy container is the **only exit** and enforces the
allowlist. A direct bypass (a direct IP, `--noproxy`) is therefore impossible —
the container simply has no route to the outside.

```bash
make egress-image          # builds covey-egress:latest (the proxy container)
COVEY_SANDBOX_PROVIDER=docker
COVEY_EGRESS_ENFORCE=true
COVEY_EGRESS_ISOLATION=network
COVEY_EGRESS_ALLOW="helpdesk.example.com"   # target-system hosts (UI hosts are added)
```

The control plane sets up the network and the proxy container automatically
(image `make egress-image`), **one set per runner**:
`covey-egress-internal-<runner>` and `covey-egress-proxy-<runner>`. A runner
serves exactly one organisation, so two tenants never share an internal segment
— `--internal` cuts the way out, not the way sideways. Whoever operates a single
organisation sees one of each, as before.

The proxy identifies the requesting agent through a per-sandbox token (proxy
authorization, set on wake) and applies that agent's effective allowlist, with a
~15 s cache. It fetches that allowlist **from the control plane**
(`COVEY_CONTROL_URL` + `COVEY_RUNNER_TOKEN`, endpoint `/api/runner/v1/…`) and no
longer from Postgres: the proxy is an enforcement point, not a database client,
and on a remote runner the old construction would mean distributing the database
credentials to every host that runs sandboxes (`spec/16-runner.md`).

Two consequences for operations: the control plane has to be reachable from the
proxy container (it is attached to the bridge network for that anyway), and the
proxy container is renewed once per control-plane start — its runner token is
rolled at every start, and a leftover container would carry the old one.

**Prerequisite:** the control plane has to be reachable over **TLS/wss**. In
network mode the daemon↔control-plane WebSocket also runs through the proxy by
HTTP CONNECT — that only works with `wss://` (a TLS tunnel), not with
plaintext `ws://`. `host.docker.internal` is on the allowlist automatically for
this.

**Verified** (with real containers): per-agent enforcement (agent A reaches only
its hosts, agent B only its own), a blocked host 403, a wrong/missing token
407, a direct bypass fails, the decision log correct per agent. **Not**
verified end to end in the repo: the complete agent run (the coveyd WS through
the proxy + the runtime) — that needs the sandbox image, a wss control plane and
real credentials; check it against the target environment.

Secrets (per agent, in the SecretStore, **not** as env): `zammad_url`,
`zammad_token`, `anthropic_api_key`/`claude_code_oauth_token`.

> Signature check: an **empty** `COVEY_ZAMMAD_WEBHOOK_SECRET` disables the
> check (dev only). Always set it in production.

---

## 7. Known limits / production checklist

The MVP vertical slice works, but before real customer traffic these points
have to be considered (details and file references below). Prioritised:

**Blockers for production use with real customer data:**

1. **Egress enforcement (implemented, two stages, on by default).** With the
   `docker` provider the sandbox traffic goes through a fail-closed
   allowlist proxy (`internal/egress`). Two modes (`COVEY_EGRESS_ISOLATION`):
   - `proxy` (default): cooperative via HTTP(S)_PROXY — it prevents naive
     exfiltration but can be circumvented by the agent through direct IPs.
   - `network`: **hard isolation** — the sandbox on an internal Docker network
     without internet, the proxy container as the only exit. A direct bypass is
     thereby impossible (verified). Setup see above.

   **Still open:** (a) network mode requires the control plane over TLS/wss
   (the WS runs through the proxy by CONNECT); (b) the LLM key is still passed
   into the sandbox as an env var — injecting it at the proxy instead (the key
   never in the sandbox) remains the next hardening step; (c) the `local`
   provider cannot isolate in principle.
2. **A lost connection = a lost ticket.** Every error (including a network blip)
   sets the task hard to `failed`; no retry/backoff, no daemon reconnect, a
   one-sided heartbeat without a timeout. → reset transient errors to
   `open`/`blocked`, reconnect + a two-sided heartbeat.
3. **Startup reconcile (implemented).** At `serve` start, orphaned
   `in_progress` tasks (whose sandbox vanished with the last process) are reset
   to `open` so that they take hold again immediately after a crash/deploy.
   This applies to a single node; cross-node session liveness remains open for
   real HA.
4. **The budget caps only reactively.** Costs arrive only *after* the run; a
   runaway task can blow the budget, and only the next one is throttled. →
   pass streaming cost + `MaxBudgetUSD` through to the CLI.

**Hardening shortly after:**

5. **The `blocked` mechanic is prompt-dependent.** If the `COVEY_STATUS` line is
   missing, the task is silently counted as `done`. → a structured tool call
   instead of a parsed text line, with a safe default (invalid → `blocked` +
   escalation).
6. **The signing key is process-local + volatile**, session maps are in memory →
   the control plane is effectively single-node (even though the DB building
   blocks suggest HA). → persist the key; either honestly single-node (leader
   election) or sessions across nodes.
7. **Memory is a `HashEmbedder`**, not a real embedding → only keyword, no
   semantic similarity.
8. **No rate limiting/lockout** on the login + webhook endpoints.
9. **`webhook_events` without retention** — the table grows unbounded, plan
   periodic cleanup.
10. **"Brokered short-lived" is currently cosmetic:** the long-lived Zammad
    token is passed through with a purely informational TTL and cached by the
    daemon for the whole session. Fine for the built-in store according to the
    spec, but the TTL enforces nothing.

---

## 8. Outlook

- **Queue→agent routing in covey**, so that a single webhook distributes tickets
  onto different agents based on their group (today: mapping through the
  slug URL; by polling, each agent's own assignment is already its queue).
- **A declarative intake filter** as with the manifest plugins
  (`Webhook.IgnoreWhen`) for the compiled Zammad plugin — field-based rules
  over priority/state/tag, not just the group.
