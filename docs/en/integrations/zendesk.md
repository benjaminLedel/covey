---
slug: zendesk
title: Zendesk Support
description: 'A Zendesk queue as a covey intake: the four credential forms, the ticket as the unit of work, the conversation rebuilt from the audit trail, and what an agent is allowed to write back.'
---

A runbook for running a **Zendesk Support** queue with a covey agent. The plugin
ships in the [plugin pack](https://github.com/benjaminLedel/covey-plugin-pack)
as `zendesk/`; the default covey binary imports it, so there is nothing to
build or install — enable the target system, deposit the credential, pick an
intake.

> Short version: one credential in the SecretStore
> (`zendesk_url` + `zendesk_token`), one of four auth forms in the token value,
> and one of two intakes (heartbeat without any Zendesk setup, or a signed
> webhook). The ticket is the unit of work. The agent reads through
> `/tickets/<id>/audits.json` rather than `/comments`, so it sees its own
> internal notes and cannot read the thread out of order.

If you are looking at the other helpdesk in this pack: Zammad is documented in
[`zammad.md`](zammad.md). The two do the same job and differ in three places —
the credential, how a conversation has to be read, and what "escalate" means.

---

## 1. The data flow

```
Zendesk ──(trigger webhook, signed)──────►  covey  /api/webhooks/zendesk/<agent-slug>
   │                                          │  verify signature → intake rule → backlog task
   │                                          ▼
   │                                        agent (sandbox)
   │                                          │  actions through the action proxy
Zendesk ◄──(REST /api/v2, brokered token)──────┘  list_tickets, list_messages, reply, escalate
   ▲
   └─(heartbeat, no Zendesk setup)── the pre-check asks: is anything waiting for us?
```

Two directions, two credentials:

- **Outbound** (covey → Zendesk): REST against one host, the account root,
  authenticated with a brokered credential that never lands in the sandbox.
- **Inbound** (Zendesk → covey): a trigger webhook carrying a signing key, or no
  inbound path at all if the agent works the queue by heartbeat.

---

## 2. Setting it up

### 2.1 In Zendesk: a least-privilege identity

Create a dedicated user ("covey agent") rather than using your own:

- **Agent role**, no admin rights. A token with `read:messages` would let the
  holder write as any end user; the point is that this one cannot.
- Put it in **exactly the group(s)** the agent is to work. On Team and Growth
  plans group restrictions do not exist — read the limits in section 7.
- If the agent is to move tickets into an escalation group, give it a second
  trigger and make that group a real destination.

### 2.2 In covey: the credential

Two secrets, per agent, in the SecretStore:

| Secret | Value |
|---|---|
| `zendesk_url` | `https://acme.zendesk.com` — the account root, **without** `/api/v2` |
| `zendesk_token` | one of the four forms below |

The token value says which auth form to use; the plugin recognises the form from
the value itself:

| Form | Value | Notes |
|---|---|---|
| API token | `<email>/token:<api-token>` | The simple case. `/token:` is **literal** — it is what the plugin recognises the form by, and Basic auth is what Zendesk expects behind it. The address in front has to be a real agent |
| OAuth client | `client:<client-id>:<client-secret>` | Mints a token per need, refreshes on its own. For a long-lived installation this is the form to pick |
| OAuth refresh token | `refresh:<client-id>:<client-secret>:<refresh-token>` | Works, but rotating burns the old value and the plugin cannot write back into the SecretStore — see section 6 |
| Access token | `<token>` | Anything the shapes above do not match is used as it stands: a token minted elsewhere, or a test. The plugin never refreshes it |

**The separators are colons, and they are exact.** A value that misses them does
not fail loudly — it falls into the last row and is sent as a bearer token, and
Zendesk answers `HTTP 401: invalid_token`. That error says the token is wrong;
what is wrong is the shape.

An agent that is only to look after **one** group names it in the URL:

```
zendesk_url = https://acme.zendesk.com queue="Support L1"
```

The name is the one `list_groups` reports, and the quotes matter — group names
have spaces. This is a **boundary, not a default**: the agent sees that group's
tickets and no others, including a ticket addressed by the id a customer quotes,
and including everything that writes. The heartbeat pre-check inherits it, so the
agent is not woken for another group's ticket in the first place. Unlike
`COVEY_ZENDESK_INTAKE_GROUPS` (section 3) this belongs to the agent rather than
to the installation: which group is mine is a property of the employee, not of
the machine they run on.

### 2.3 Access and guard rails

The agent's `ACCESS.md` names the system and its scopes
(`system: zendesk scope: read,write,comment`). Writes that go out to a customer
are gated separately from writes that stay inside the ticket, and escalation is
its own gate — see the table in section 5.

### 2.4 Intake — heartbeat, webhook, or both

**By heartbeat, with no Zendesk setup at all.** In `HEARTBEAT.md`:

```yaml
alle: 15m
nur-wenn: zendesk
titel: Look after the support queue
aufgabe: Check the open tickets (list_tickets) for ones waiting for an answer,
  read the conversation (list_messages) and reply.
```

`nur-wenn: zendesk` asks one question — *does a ticket in scope wait for us?* It
costs one list read plus one read per ticket that could be waiting, at most
`COVEY_ZENDESK_PROBE_TICKETS` of them (default 10). The list is read **without a
status filter** — the endpoint takes exactly one, and "waiting for us" means new,
open, pending and hold together, so filtering client-side costs one call instead of
four and lands on the same tickets. A ticket whose newest public comment came from
our own identity does not count: an agent that has answered is not called back to
the same ticket by its own answer. A customer reply produces a new public comment,
so it is woken again. The check also returns a fingerprint of *what* is waiting, so
an agent that read a ticket and decided to write nothing is not started again a
minute later by the same state.

**By webhook**, if a ticket is to be picked up the moment it arrives. In the
Admin Center (*Apps and extensions → Trigger and automation webhooks*):

- **Endpoint URL**: `https://covey.example.com/api/webhooks/zendesk/<agent-slug>`
  — the slug of the responsible agent; the agent id (the UUID in the agent page's
  URL) works too and is the right choice on an installation with several
  organisations, since a slug is unique only within one.
- **Request method** POST, content type `application/json`.
- **Signing: enable**, and use the value of `COVEY_ZENDESK_WEBHOOK_SECRET` as the
  signing key.

Then subscribe it: either an Event trigger on *Ticket create* / *Ticket update*
(the account posts a ticket event, subject `zen:ticket:<id>`), or a
Trigger/Automation that posts the ticket itself. **Both payload shapes are
understood**, and which one arrives is decided by the subscription, not by a
setting here.

> **The signature is what makes this deliverable trusted.** Zendesk posts
> `x-zendesk-webhook-signature: t=<unix-seconds>,v1=<hex>` with an HMAC-SHA256 over
> `t + "." + body`, and the plugin recomputes it, in hex, with the secret. A stamp
> more than five minutes from the platform clock is refused, so a captured delivery
> cannot be replayed — the skew window is deliberately the same convention as the
> platform's own `WEBHOOK_MAX_SKEW`, so a deployment that wants to stop trusting a
> timestamp that far back has a knob for it, and it lives with the platform.
>
> An **empty** `COVEY_ZENDESK_WEBHOOK_SECRET` switches the check off, which is how a
> development setup is marked — exactly as with Zammad. On a production installation
> the secret is set, and a webhook that arrives unsigned is then refused rather than
> run. A local instance that cannot sign has to be driven by heartbeat instead.

### 2.5 Process env

```bash
COVEY_PUBLIC_URL=https://covey.example.com          # reachable from Zendesk, not localhost
COVEY_ZENDESK_WEBHOOK_SECRET=<long-random-string>   # identical to the signing key
```

### 2.5b Two steps the wizard does not take for you

Both are the same kind of trap: everything reads as done, and the first run gets
nothing.

- **Assign the secrets to the agent.** The setup assistant stores `zendesk_url`
  and `zendesk_token` at the **organisation**, and an organisation secret reaches
  an agent only on an explicit assignment. *Agent → Settings → Secrets → assign
  org secret*, both values. Without it the connection test says
  `no zendesk_token stored for this agent` and a run gets no credential at all.
- **Open the egress for the account host.** The Zendesk actions run in the
  agent's sandbox, and the proxy in front of it blocks fail-closed everything
  that is not listed. *Agent → Settings → Egress → own hosts*, add
  `<subdomain>.zendesk.com`. Without it the credential is right and every call
  still ends nowhere.

### 2.6 Testing

1. Create a ticket in the target group **as a customer**.
2. A backlog task at the agent? → read the recording.
3. If the agent answered: is the answer **visible to the customer** (section 5)?
4. Ask a follow-up as the customer: does the agent wake again, and does the
   earlier internal note appear in what it reads?
5. `escalate`: did the ticket move to the escalation group *and* keep its tags?

A ready-made agent that uses all of this:
[`examples/zendesk-support-agent.bundle.json`](../../../examples/README.md) — a
support agent that works the queue by heartbeat. It needs nothing but the two
secrets from 2.2 and the target system enabled.

---

## 3. Which tickets the agent takes up

Three filters, from the source inward. Each one is a different question.

| Level | Where | Question |
|---|---|---|
| Trigger | Zendesk | Which events are delivered at all — group, priority, channel, tag |
| `COVEY_ZENDESK_INTAKE_GROUPS` | covey, per installation | Which groups this installation works on. Empty = every group |
| `queue=` in `zendesk_url` | covey, per agent | Which group **this agent** owns — a boundary, also for reads by id and for writes |

The cleanest filter is at the source: what a trigger does not deliver never
reaches covey. The env var is the safety net for a trigger drawn too widely, and
unlike the Zammad equivalent it exists here because the heartbeat pre-check has to
carry the same restriction without any webhook at all.

The group filter applies to **tickets that carry a group**. A ticket without one
is not "in every group" — it belongs to none, and a queue-shaped agent has no
business with it. The same goes for the heartbeat: tickets without a group do not
wake an agent that works by group. An installation that wants those worked too has
to route them into a group.

---

## 4. What the agent reads

**The conversation is rebuilt from the audit trail** (`/tickets/<id>/audits.json`),
not from `/comments`. Three reasons, all of them things that go wrong otherwise:

1. Internal notes are real answers. On the audit trail they are first-class
   events, and an agent cannot miss an older answer because it read only the last
   three comments.
2. Audits arrive newest-first. A thread read newest-first makes an agent answer the
   wrong question. The plugin reverses the events before the agent sees them.
3. It is the only place where "when was this hidden, and by whom" can be seen at
   all. Redacted comments are reported as redacted rather than silently missing.

Comments written through the ticket's own `comments[]` array (the usual way) do not
appear as audit events. The plugin folds them into the timeline from the ticket
body when it knows the comment id, so the sequence stays intact.

**Names.** The agent works in names, not in numbers: groups, requesters,
assignees and collaborators come back resolved, from one batched
`/users/show_many.json` per list. A person who is not visible to this credential
is reported by id rather than as a name that was guessed. If `/users/me` is not
readable, nothing fails: the read returns the names it could get, and the
heartbeat's *"is this our own answer?"* test falls back to the sender's role —
which is the weaker of the two tests, and the plugin knows it.

**Attachments.** `list_attachments` gives id, name, type, size and author — and no
download URL, because those are signed and short-lived. `download_attachment`
fetches the file and hands back a path inside the sandbox
(`attachments/<attachment-id>-<name>`, so two files of the same name on one ticket
do not overwrite each other), where the runtime's vision step can look at it. A
file from a foreign host is refused, and a `file://` path in an answer is refused:
the one address the agent is not allowed to fetch is a local file.

---

## 5. What the agent writes

| Action | Goes out | Guard-rail subject |
|---|---|---|
| `reply` (`internal:true`, the default) | private comment | `zendesk:reply_internal` |
| `reply` (`internal:false`) | public comment — the customer sees it | `zendesk:reply_external` |
| `update_ticket` | the fields it names: status, priority, tags, assignee, group, custom fields | `zendesk:update_ticket` |
| `set_status` | one field, so moving a ticket on does not mean naming a whole ticket | `zendesk:set_status` |
| `escalate` | an internal note with the reason, the escalation group if one is set, the `covey-escalated` tag | `zendesk:escalate` |
| `create_ticket` | a new ticket, its body as the first comment | `zendesk:create_ticket` |
| `attach_file` | a file from the sandbox and the comment that carries it | `zendesk:attach_file` |
| `merge_tickets` | the duplicate, folded into the ticket that survives | `zendesk:merge_tickets` |

The scope vocabulary of this system is `read`, `write`, `comment`; the guard rails
match on the **subject** above, which is what a run records and what the control
plane later asks about. Writes that leave the ticket and writes that stay inside it
are therefore two different gates, and so is escalation.

The default for `reply` is **internal**. A wrong answer is a note then, not a
statement made to a customer — an agent that means to answer writes
`"internal": false` explicitly.

**A reply does not change the status unless you tell it to.**
`COVEY_ZENDESK_REPLY_STATUS=pending` makes an outgoing answer settle the ticket to
that status; unset, the status is left alone and the answer says so. The status of
a live ticket belongs to the workflow, and an agent that quietly moves it on
hides a follow-up. If the settle fails — an automation already moved it, the
workflow forbids the jump — the answer still reports itself as sent and carries
`status_warning` with what the account said: the reply went out, and pretending
otherwise would have an agent send it twice.

**`escalate`** writes an internal note with the reason, moves the ticket into
`COVEY_ZENDESK_ESCALATION_GROUP` when one is configured (empty = it keeps its
group), and merges in the `covey-escalated` tag. It does not touch the priority:
a human deciding that a ticket is urgent is allowed to still be the one who said
so. The tag is merged rather than written as a whole list, because a tag update
*replaces* the list — escalating a ticket by silently deleting the tags another
department put on it would be a poor kind of help.

**A blocked agent.** The intended pair, as with Zammad: the agent answers with a
question and sets the status to `pending`. The customer's reply arrives as a new
public comment, the trigger fires, covey correlates by ticket id and resumes the
session. A trigger on *ticket created* only means the agent never wakes again —
subscribe to updates as well.

---

## 6. Credentials that expire

The OAuth forms mint a token that expires. The probe — the identity line the
credential UI shows — reports the expiry the account gave with that mint. For a
token this process did not mint there is nothing to report, and the plugin says so
rather than guessing a date.

**Client credentials** (`client:`) is the form for anything meant to last: the
plugin mints per need, keeps nothing, and there is nothing to rotate.

**Refresh token** (`refresh:`) burns the old value on every refresh. The plugin
keeps the new one in memory for the life of the process and cannot write it back
into the SecretStore from inside a call. The way to rotate is the platform's:
`Rotate` performs one refresh and **returns** the new credential, and the control
plane stores the result. A process restart with a burned refresh token is a
credential that no longer works — which is why the setup doc in
`zendesk/plugin.go` points at client credentials for long-lived setups.

---

## 7. Env reference

| Variable | Default | Meaning |
|---|---|---|
| `COVEY_PUBLIC_URL` | `http://localhost:8494` | The base URL Zendesk delivers the webhook to |
| `COVEY_ZENDESK_WEBHOOK_SECRET` | *(empty = check off, dev only)* | Signing key, identical to the webhook's signing key |
| `COVEY_ZENDESK_INTAKE_GROUPS` | *(empty = every group)* | Groups this installation works on, by name |
| `COVEY_ZENDESK_ESCALATION_GROUP` | *(empty = keep the group)* | Where `escalate` moves a ticket, by name |
| `COVEY_ZENDESK_REPLY_STATUS` | *(empty = status untouched)* | One of `new open pending hold solved closed canceled` — the status an outgoing answer sets |
| `COVEY_ZENDESK_ATTACHMENT_MAX_MB` | `25` | Per file, 1…50 — Zendesk's own cap |
| `COVEY_ZENDESK_PROBE_TICKETS` | `10` | How many tickets the heartbeat pre-check reads |

Every queue-shaped setting is configured **by name**, and the names are what
`list_groups` reports — run that action once instead of copying strings out of the
admin UI.

**Egress.** The account root is the only host the plugin ever contacts, and OAuth
tokens are minted from that same host (`https://<subdomain>.zendesk.com`), never
from a generic authorisation host. So one allowlist entry per agent:

```bash
COVEY_EGRESS_ALLOW="acme.zendesk.com"
```

> **`zendesk_url` is https**, and plain http is accepted only on a loopback address
> (127.0.0.1, localhost, ::1) — which is how a local instance and the scripted live
> test run. Everything that is not a laptop is refused at the credential, before a
> client pair could go out over plaintext.

---

## 8. Known limits

- **Audits pagination reads every page.** `/tickets/<id>/audits.json` has no
  server-side window, so a ticket with hundreds of audits costs one read per 100
  events. Threads of that length are rare in support and the plugin does not
  pretend otherwise; `limit` trims what reaches the agent, not what is read.
- **`update_time` in search results** arrives as unix seconds on the index and as
  RFC 3339 everywhere else. Times are read leniently and kept as the string that
  came in.
- **Team and Growth plans** have no group restrictions: the credential sees every
  ticket. `queue=` then keeps the *agent* honest but is not enforceable — do not
  treat it as a security boundary on those plans.
- **A ticket without a group** is not woken and not listed by a queue-shaped
  intake. Unassigned-and-ungrouped is a state some instances use for spam; if it
  carries work, route it into a group.
- **The wake bucket is per action, not per ticket.** The subject a run records is
  `zendesk:<action>` (`reply` split into internal and external), and that is the
  string `WritesWorkSignature` is asked about afterwards. Two agents answering two
  different tickets therefore share one bucket and can serialise on one watermark.
  A per-ticket bucket would need the signature question to carry the id, and the
  subject this plugin answers with is shaped for the action instead — the same
  choice Zammad makes, for the same reason.
- **Redacted comments** are reported as redacted. Their text is gone at the source,
  not hidden by the plugin.
