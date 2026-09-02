---
slug: jira
title: Jira
description: 'Connecting covey to Jira Cloud or Server/Data Center: API token, webhook, intake projects — the unit of work is the issue.'
---

A practical runbook for the target system **Jira**
(`github.com/benjaminLedel/covey-plugin-pack/jira`). The unit of work is the
**issue**; the plugin covers Jira **Cloud** and Jira **Server/Data Center**.

> Short version: an API token (Cloud) or a personal access token (Data Center),
> stored as `jira_token` + `jira_url`. The agent finds its issues **itself**
> (`search_issues` with JQL), driven by a `HEARTBEAT.md` entry — and, where the
> site may post, by a **webhook** on top. These are **configuration steps**, not
> a rebuild.

Jira differs from the other ticket systems in covey in one way that shapes
everything below: **it does not hold the code.** A developer agent works Jira
*and* GitLab or GitHub, and what ties the two together is the issue key
(section 5). Whoever sets Jira up alone has given the agent a board and no
workshop.

## Cloud or Data Center

One plugin, two deployments. Which one is spoken to is inferred from the
**shape of the token** — nothing else:

| | Jira Cloud | Jira Server / Data Center |
|---|---|---|
| `jira_token` | `mail@example.com:<API token>` | `<personal access token>` |
| HTTP auth | Basic | Bearer |
| REST version | `/rest/api/3` | `/rest/api/2` |
| Long texts | **ADF** (a JSON document tree) | wiki markup (a string) |
| Search endpoint | `POST /search/jql` | `POST /search` |
| The assignee field | `accountId` | `name` |

The assignee row is the one that used to bite: neither value is what a person
writes, and neither is what stands on the ticket. The plugin therefore resolves
a display name, a mail address or a login against the site's own user search
before it assigns — an accountId is passed through untouched, and an ambiguous
name comes back as an error naming the candidates rather than a guess.

A pair with a colon is Cloud, a single value is a personal access token. An
installation where that inference is wrong writes it out:

```
jira_url = https://jira.acme.example auth=bearer api=2
```

The connection test on the plugin page reports which of the two it decided on —
that is the point of showing it. A Data Center addressed as Cloud does not fail
at the test; it fails much later, on the first comment, whose body is then
stored as the literal text of a JSON tree.

---

## 1. Overview of the data flow

There are two routes in, and either on its own is enough.

**a) By heartbeat (polling)** — works behind NAT/a firewall, needs no public
URL:

```
covey  ──(heartbeat tick)──►  backlog task "work the Jira board"
                                 │
                                 ▼
                              agent (sandbox, Claude Code)
                                 │  actions through the action proxy
Jira   ◄──(REST /rest/api/3)─────┘  search_issues → get_issue/list_comments → transition/comment
```

**b) By webhook** — real time, needs a publicly reachable covey
(`COVEY_PUBLIC_URL`):

```
Jira ──(POST /api/webhooks/jira/<agent-slug>)──►  covey
        X-Hub-Signature (HMAC-SHA256)               │ verify → deduplicate → correlate
                                                    ▼
                                        new task  or  wake a blocked task
```

The division of labour between the two is deliberate and is what lets you run
both at once:

- A **comment** and a **new issue** become a **task**.
- An **assignment** becomes a task as well — being handed a ticket is how work
  arrives without anybody writing a sentence.
- Everything else — a status change, an edited field — only **wakes a task that
  is already waiting** for that issue (`CorrelateOnly`). If nobody is waiting,
  it is not work: an agent started by every edit of a ticket it is not working
  on is an agent nobody leaves switched on.

---

## 2. Step-by-step instructions

### 2.1 In Jira: an account and a token

Give the agent **a user of its own** (`covey-bot`). Every comment, every status
change and every commit link carries that name, and a person looking at the
board should be able to tell an agent's move from a colleague's.

Rights: browse projects, add comments, transition issues, assign issues, edit
issues — for **exactly the projects** it is to work, no more. On Cloud that is a
project role; on Data Center a permission scheme.

**Cloud:** log in as that user → `id.atlassian.com` → *Security → API tokens →
Create API token*. The token is shown once, and it **expires** — a year at most,
and Atlassian offers no API to renew it. Enter the date on the secret in covey
(*Secrets → jira_token → Set expiry*): covey warns two weeks ahead, on the
agent's page and by mail, and a token Jira refuses is marked the moment a run
hits it rather than three weeks later.

**Server/Data Center:** as that user → *Profile → Personal Access Tokens →
Create token*. Here covey reads the expiry from the instance and **renews the
PAT itself** a month before it runs out — the connection test shows the date
and says so.

### 2.2 In covey: deposit the secrets

Set per agent under *Secrets* (or through the API):

| Secret | Value | Purpose |
|---|---|---|
| `jira_url` | `https://acme.atlassian.net` | the site, **without** `/rest` |
| `jira_token` | `covey-bot@acme.example:<API token>` | Cloud |
| | `<personal access token>` | Server/Data Center |

`jira_url` carries optional components after the URL, separated by spaces:

```
jira_url = https://acme.atlassian.net project="ACME" api=3 auth=basic
```

**`project=` is a boundary, not a default.** An agent whose credential names a
project sees that project and no other — through `search_issues`, through
`get_issue` with a key somebody quoted at it, and through everything that
writes. Several: `project="ACME,OPS"`.

It sits in the credential rather than in the process environment on purpose:
`COVEY_JIRA_INTAKE_PROJECTS` narrows a whole installation, and *which project is
mine* is a property of the employee, not of the machine they run on.

The wall costs nothing to enforce — a Jira key carries its project in front of
the hyphen, so no call is needed to know where `ACME-17` belongs. And it is
applied to a **search** by bracketing the agent's own query:

```
your query:   status = Open OR reporter = dana ORDER BY updated DESC
what is sent: project in (ACME) AND (status = Open OR reporter = dana) ORDER BY updated DESC
```

Appended behind the `OR` instead, the condition would bind to the last term
only — a wall with a hole exactly where somebody used an `OR`.

### 2.3 In covey: enable the target system

In the agent's `ACCESS.md`:

```markdown
- system: jira scope: read,write,comment
- system: gitlab scope: read,write,comment
```

The second line is not decoration. Jira holds the ticket, GitLab (or GitHub)
holds the code, and a developer agent needs both — see section 5.

| Scope | What it permits |
|---|---|
| `read` | `search_issues`, `get_issue`, `list_comments`, `list_transitions`, `list_projects`, `list_attachments`, `download_attachment` |
| `comment` | `comment` |
| `write` | `transition`, `assign`, `update_issue`, `create_issue`, `link_issues`, `log_work`, `attach_file` |

The prompt documentation is narrowed to the scopes granted. That is not
cosmetic: the doc stands in the context of **every turn**, so a procedure the
agent cannot carry out is not paid for once but on every one.

### 2.4a Setting up the intake by heartbeat

In the agent's `HEARTBEAT.md`:

```markdown
- alle: 15m nur-wenn: jira:assigned titel: Work the Jira board
  aufgabe: Look at the issues assigned to you (search_issues
    "assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC"),
    pick up the newest and check with list_comments whether your questions have
    been answered.
```

`nur-wenn:` is a **pre-check in the control plane**: the credential never leaves
it, no sandbox is started, and the (expensive) agent wake is skipped when there
is nothing waiting. Three sub-scopes:

| `nur-wenn:` | Fires when … |
|---|---|
| `jira:assigned` | an issue assigned to the agent is not done |
| `jira:unassigned` | an unassigned issue in scope is not done — for an agent that is to **pick work up** rather than wait for it |
| `jira` | either of the two (the wider check) |

The gate is **edge-triggered, not level-triggered**. It remembers a signature
built from the keys and `updated` timestamps of what it found, and fires again
only when that signature *changes*. So the agent may read a ticket, decide there
is nothing to do and end its run in silence — the same state will not start it
again in the next interval. When somebody comments, `updated` moves and it
wakes.

### 2.4b Setting up the intake by webhook

*Cloud:* Settings → System → **WebHooks** → Create.
*Data Center:* Administration → System → **WebHooks**.

| Field | Value |
|---|---|
| URL | `https://covey.example.com/api/webhooks/jira/<agent-slug>` |
| Events | Comment created, Issue created, Issue updated |
| JQL | `project = ACME AND assignee = covey-bot` |
| Secret | a random string |

**The JQL filter is the sharp instrument here.** It decides which issues reach
the agent at all, and it does so in Jira, where the person who owns the board
can see and change it — better than any filter on this end.

The secret makes Jira sign the body (HMAC-SHA256, `X-Hub-Signature`), and covey
checks it. The same value goes into `COVEY_JIRA_WEBHOOK_SECRET` on the control
plane. An empty secret switches the check off — for local tests, and only
there. An automation rule that assembles the call itself may send the signature
as `X-covey-Signature` instead; both headers are accepted.

Set `COVEY_JIRA_BOT_ACCOUNT` (section 8) as well. Without it the agent's own
comment comes back through the webhook and wakes it to read its own sentence:
noise that costs a run every time.

### 2.5 Choosing realistic intervals

15 minutes is a good default for a board somebody works alongside. Below 5
minutes the pre-check itself becomes the load — it is one search per interval
per agent. With a webhook in place the heartbeat is the **safety net**, not the
main road: 30 or 60 minutes are enough then.

### 2.6 Testing

1. **Connection test** on the plugin page: it names the account and the
   deployment (`covey Bot (covey-bot@acme.example) · Cloud · ACME`). Read both
   halves — the identity is what every action will carry, the deployment is an
   inference worth checking.
2. Assign a test issue to `covey-bot`, wait for one interval.
3. In the recording: `search_issues` → `get_issue` → `transition`. What the
   agent saw is in the action results.

---

## 3. Statuses: a transition, not a field

**A status in Jira is not set. It is reached.** Which moves exist depends on the
workflow *and* on where the issue currently stands, and each of them has a
numeric id that differs from project to project.

The plugin resolves that so the agent does not have to:

```json
transition {"issue_key":"ACME-17","to":"In Progress"}
```

`to` takes the **name of the transition** ("Start Progress") or the **name of the
target status** ("In Progress"), case-insensitively; a numeric id works too. If
the name does not resolve, the error says what the workflow *does* offer right
now — that is what stops the agent guessing a second time:

```
"Deployed" is not a transition available on ACME-17 right now —
the workflow offers: Start Progress → In Progress; Done
```

Two optional parameters:

- `comment` — posted **with** the transition, in one call and one history entry.
- `resolution` — for workflows that demand one on the closing move. A workflow
  whose screen does not carry the field answers *"Field 'resolution' cannot be
  set"*; then leave it out.

`list_transitions` shows what is possible, including which fields a move
requires.

---

## 4. Fields: names, not numbers

```json
update_issue {"issue_key":"ACME-17",
              "fields":{"priority":"High","Story Points":3},
              "add_labels":["backend"],"remove_labels":["triage"]}
```

Fields are named **the way they are named on the screen**. A custom field is
resolved through the instance's own field catalogue, so `"Story Points"` works
without anybody having to know that this instance calls it
`customfield_10016`. The catalogue is cached for 30 minutes per site.

Two things the plugin does silently and deliberately:

- **Objects instead of scalars.** `"priority": "High"` is rejected by Jira;
  `{"name": "High"}` is not. The plugin wraps the fields that need it.
- **Labels are added and removed, never replaced.** Two agents on one board
  would otherwise overwrite each other's labels, and a label somebody put on by
  hand is not the agent's to drop.

---

## 5. The developer loop across two systems

This is the section that decides whether Jira is useful to a developer agent or
merely present.

```
Jira                                 GitLab / GitHub
─────────────────────────────────    ─────────────────────────────────
ACME-17 "Importer drops rows"
  │
  ├─ assign {"assignee":"me"}
  ├─ transition {"to":"In Progress"}
  │                                   checkout
  │                                   branch ACME-17-null-check
  │                                   commit "ACME-17 guard the null case"
  │                                   create_merge_request
  ├─ comment  (the MR link)  ◄────────┘
  ├─ transition {"to":"In Review"}
  │
  │                                   (review, merge)
  └─ transition {"to":"Done"}
```

Four rules the agent's `PLAYBOOKS.md` should carry, because the prompt
documentation states them but a playbook is what makes them habit:

1. **Take the ticket on before starting.** `assign` + `transition` to the
   in-progress status. A person looking at the board has no other way of seeing
   that it is being worked on.
2. **Begin every commit message with the key** — `ACME-17 guard the null case`
   — and name the branch after it. That prefix is what makes the branch, the
   commits and the merge request appear on the Jira ticket. It requires the
   repository to be connected to Jira (Cloud: *Apps → GitLab/GitHub for Jira*;
   Data Center: the DVCS accounts under *Applications*). Without that connection
   the prefix is still worth keeping: it is what a human greps for.
3. **Comment the merge request URL on the ticket.** The link on the ticket is
   the only trace a reader of the ticket has of the agent's work. The
   development panel shows it only where the connector is set up; a comment
   shows it everywhere.
4. **A question for the reporter is a comment, then the end of the run.** The
   agent goes `blocked`, and the answer wakes it — by webhook in seconds, by
   heartbeat at the next interval. Correlation key: `jira:issue:ACME-17`.

---

## 6. Attachments: look, do not guess

A bug report with a screenshot is answered by **looking at it**:

```json
list_attachments {"issue_key":"ACME-17"}
download_attachment {"attachment_id":"10412"}
```

On Cloud the content link redirects to `api.media.atlassian.com`, so that host
belongs in the agent's egress allowlist beside the site itself — without it the
download dies in the proxy, and what the agent reports is that it cannot see the
picture.

The file lands under `<workdir>/attachments/`, and the result carries the hint
that makes the agent open it with the Read tool — which for an image means
vision, not a guess from the surrounding text.

The way back is `attach_file {"issue_key":"ACME-17","path":"evidence.log"}`: a
log, a screenshot of the reproduction, a diff. Say in the comment that it is
there — an attachment nobody is pointed at goes unseen.

Both directions are capped by `COVEY_JIRA_ATTACHMENT_MAX_MB` (default 25). A
download is refused on the **metadata**, before the body is pulled: the point of
a limit is not to hold the file in memory first and count afterwards.

---

## 7. What the agent sees instead of ADF

On Cloud every description and every comment is stored as a document tree. The
plugin renders it to Markdown on the way in and builds it from Markdown on the
way out. That is not cosmetic:

- **In:** an ADF description is roughly ten times the size of the sentence it
  carries, and it stands in the agent's context in full, on every turn the issue
  is in view.
- **Out:** an agent asked to produce ADF itself produces *almost*-ADF, gets a
  400 it cannot learn from, and tries again with a slightly different tree.

What survives the round trip: paragraphs, headings, bullet and numbered lists,
fenced code blocks, block quotes, inline code, bold, italics, links, mentions,
and a pointer to each embedded attachment. What does not: tables come back as
Markdown-ish rows, panels as a `[note] …` prefix, and an unknown node type keeps
its text but loses its formatting. The sentence is what matters; a formatting
that was not recognised is a smaller loss than a comment that does not get
posted.

On Server/Data Center none of this happens — the text goes as it stands, and
the agent's Markdown is stored as Markdown (Jira renders wiki markup there, so
`**bold**` stays visible as asterisks).

---

## 8. Env reference (Jira-relevant)

| Variable | Default | Effect |
|---|---|---|
| `COVEY_JIRA_INTAKE_PROJECTS` | *(empty)* | allowlist of project keys that may become work at all — applies to the heartbeat pre-check and the webhook intake. Empty = every project. Installation-wide; the per-agent wall is `project=` in `jira_url`. |
| `COVEY_JIRA_BOT_ACCOUNT` | *(empty)* | the agent's own account (name, mail or `accountId`). An event this account caused is registered but wakes nobody. Unset, every event wakes — fail-open, because a missed question from a human is the more expensive mistake. |
| `COVEY_JIRA_ATTACHMENT_MAX_MB` | `25` | per file, in both directions (1…1024). |
| `COVEY_JIRA_WEBHOOK_SECRET` | *(empty)* | the shared secret for the webhook signature. Empty = the check is off. Read on the **control plane**, not in the sandbox — the intake never reaches the plugin's action side. |

These are read **in the sandbox**, where the action proxy runs the plugin, and
covey carries them there because the plugin declares them.

---

## 9. Troubleshooting

| Symptom | Cause | Remedy |
|---|---|---|
| `HTTP 401` right at the connection test | Cloud token used without the mail address, or a PAT sent as Basic | `jira_token` = `mail:token` for Cloud, the bare token for Data Center; `auth=` writes it out |
| A comment appears as `{"type":"doc"…}` in Jira | a Cloud site addressed as Data Center | add `api=3` (and check the token shape) |
| `HTTP 410` on a search | a Cloud site addressed as v2 — `/rest/api/2/search` is gone there | `api=3` |
| `HTTP 404` on every call | `jira_url` carries `/rest/api/…` or a trailing path | the site URL only (the plugin cuts a `/rest/` path, but not a context path that is really there) |
| `assign`: several users match | two people on the site carry that name | take the accountId from the error message |
| `"…" is not a transition available` | the workflow does not offer that move **from the current status** | `list_transitions`, then the name it reports |
| `Field 'resolution' cannot be set` | the transition screen does not carry the field | leave `resolution` out |
| Agent wakes every interval and does nothing | `nur-wenn:` missing, or the sub-scope too wide | `nur-wenn: jira:assigned` |
| Agent wakes on its own comments | `COVEY_JIRA_BOT_ACCOUNT` unset | set it to the bot account |
| Webhook arrives, nothing happens | signature wrong, or the project outside the intake allowlist | compare the secret; check `COVEY_JIRA_INTAKE_PROJECTS` |
| `issue … lies outside your projects` | the per-agent wall | intended — widen `project=` only if the agent really is to work there |

---

## See also

- [`ops-gitlab.md`](./gitlab.md), [`ops-github.md`](./github.md) — the code
  half of the developer loop
- [`../spec/22-plugin-marketplace.md`](../../../spec/22-plugin-marketplace.md) — how
  target systems arrive at all
- [`../spec/03-lifecycle-scheduling.md`](../../../spec/03-lifecycle-scheduling.md) —
  wake sources, `blocked`, correlation
