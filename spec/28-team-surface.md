# 28 — The team surface: the day's working view

**Status: the first slice is built** (#298 — the shell, the thread, the reply at the question, the search, reactions). Section 5 is specified and open as #302. This document describes what it becomes, and it exists because the next four steps all pull on the same joint: what the team surface is allowed to show.

The console is the view of somebody who *builds* a workforce. The team surface is the view of somebody who *works with* one, and that is a different question about scope at every turn — which is why it needs writing down once instead of being decided four times.

## What is built

A shell of its own at the root: the colleagues grouped by department, what waits on top, a thread per agent. A message becomes a task, a reply becomes the resume input of a parked one, and everything else is read out of the objects that already carry it ([`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md), `internal/httpapi/chat.go`). Each agent carries a face computed from its slug, and the face shows whether it works, sleeps or has been stopped.

## 1. The org chart belongs here, as the directory

The console's org chart answers *how is this organisation built* — it is editable, it drags departments around, it assigns supervisors. That is administration, and it stays there.

Under the team the same structure answers a different question: **who do I talk to?** So it is not the same screen with the editing switched off; it is a directory that happens to be shaped like the org chart:

- read-only, no drag, no rename
- every node is a door: a click opens the conversation with that colleague
- humans are in it and are not a second class — they are simply nodes you cannot open a thread with *yet* (see 2)
- it is the picker: selecting several nodes is how a group conversation starts (see 3)

That last point is the reason to build it at all. A flat list of colleagues stops working at forty, and a search finds who you can name. The chart finds who you *cannot* name — "the person who reviews invoices in finance" is a position in a structure, not a string.

## 2. Humans in the conversation

Today a thread has exactly two sides: the person reading it and one agent. Everything else the agent produces arrives through the objects.

Bringing humans in is a bigger step than it looks, because a human is not reachable the way an agent is. An agent has a backlog; a message to it becomes a task, and the wake is the delivery. A human has no backlog and no wake. So a message to a human is either:

- **a notification** (mail, Teams, whatever the organisation runs), with the thread as the record — the platform already has the mail plumbing (`internal/notify`), or
- **an item in their inbox** on the platform, which is where every other thing that waits for a person already is ([`06-observability-control.md`](06-observability-control.md))

The second keeps one list of what waits for me and needs no channel. The first reaches somebody who does not have the tab open. They are not exclusive — the inbox item is the truth, the notification is the nudge — and that is the recommendation.

## 3. Group conversations

A thread with several participants is the point where "chat" stops being a view of one agent's backlog and becomes an object of its own. That is the decision, and it should be taken deliberately:

- **The thread stays a view.** A group conversation is then a *set* of tasks — one per agent — correlated by a shared key, and the thread merges them. Cheap, no new table, and it inherits every guard rail. What it cannot do: a message addressed to the group but to no agent in particular, and a reply by one agent that the others see.
- **The thread becomes an object.** A `conversations` table, participants (agents and humans), messages, and tasks that hang off a message. Everything a group needs, and a second place where work is described — the thing [`03`](03-lifecycle-scheduling.md) exists to prevent.

**Recommendation: the middle.** A conversation is an object, but it owns no work: it holds participants and messages, and every message that is meant as work still becomes an ordinary backlog task of the agent it is addressed to. The conversation is the envelope, the backlog stays the ledger. An agent that reads the thread reads other agents' messages as context, and nothing in the dispatcher changes.

**Open:** whether an agent may speak in a group without being addressed. That is the difference between a group chat and a swarm, and it is a guard-rail question before it is a UI one.

## 4. Several organisations at once

**The requirement:** somebody with seats in more than one organisation must see all of them in the team surface without switching. It is the daily working view; a person does not think in tenants at nine in the morning.

**Why that is not a frontend change.** Every read in this API is scoped to the *active* seat: `principalFrom(r).OrgID` decides what a query returns, and switching is `POST /auth/switch-org`, which rewrites the session. Asking twice with two sessions is not an option a browser has, and it would be the wrong answer anyway — the session is also what the audit trail records.

So the team surface needs **account-scoped reads**: a small set of endpoints that answer for every seat of the account instead of for one organisation. Three rules make that safe, and none of them may be skipped:

1. **The role is per seat, not per account.** Somebody may be `agent_owner` in one organisation and `auditor` in another. A cross-org answer applies each seat's own role to each seat's own rows — the active seat's role may never leak across the boundary. That is the whole reason this cannot be done by widening an existing handler with an `OR`.
2. **The organisation is visible on every row.** A colleague list mixing two companies without saying which is which is a mistake waiting to be made by the reader, not by the code.
3. **Writing stays single-org.** A message creates a task in exactly one organisation — the one the agent belongs to. Reading crosses the boundary; writing never does, and the existing `agentScoped` middleware keeps doing what it does.

What it costs: `GET /agents`, `GET /departments` and `GET /inbox` need account-scoped twins, and the audit trail has to record which seat a read came from. What it buys: the surface stops being a per-tenant console and becomes what the name says.

**Open:** whether the console follows. The recommendation is no — administration happens inside one organisation, and a fleet-wide admin view is a different product with a different threat model.

## 5. A message is not automatically a task

Today every message becomes a backlog task. That is right for "check the
Globex invoice" and absurd for "did that go out yesterday?" — the second gets
an isolated sandbox, a runtime seat and a line in the cost list in order to
answer one sentence. It is also slow in the direction that matters: a
colleague answers in four seconds, ours opens a ticket.

The message should be handed to the agent, and **the agent decides what kind
of thing it is**: answer in the thread, acknowledge with a mark, or open the
task. Whether "look at the invoice" is a question or a job depends on the
agent's role, and the agent is the thing that knows its role — so this is not
a rule in the interface.

**It needs an engine of its own, configured per organisation** (#302). Not
the agent's runtime: that is a sandbox plus a seat plus a wake, which is the
machinery the triage exists to avoid paying for. It is a cheap turn inside
the control plane, and covey already has that shape — `internal/httpapi/assist.go`
uses the organisation's credential server-side, without a sandbox, to help
write a config. Off is the default, and off means today's behaviour exactly.

The limits are the point, not a detail, because the turn runs **outside the
sandbox**:

- no tools, no target systems, no credentials — it reads the conversation and
  produces text; everything it would need beyond that is the reason to open a
  task instead
- its output is text or a task, nothing else: no config change, no wake, no
  approval decided
- it is recorded and counted like any other run ([`06`](06-observability-control.md))
- the guard rails on what an agent says hold for a direct answer exactly as
  they hold for a ticket reply — a cheap path must not become the cheap way
  around them

The open decisions are in the issue; the load-bearing one is what the answer
is allowed to know. The recommendation is: the role and the thread, not the
wiki memory. An answer that needs the memory is an answer that should have
been a task.

## What the surface must not become

A second console. Every one of the four steps above has a version that ends there: an editable chart, a message that can change a config, a group conversation that assigns work to departments, a cross-org view that also writes. The line is the same each time — **the team surface reads across and writes narrowly**, and anything that configures an agent stays where the config already is.

## Related

- [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md) — the backlog the thread is a view of
- [`06-observability-control.md`](06-observability-control.md) — the inbox, approvals, the recording
- [`09-enterprise-model.md`](09-enterprise-model.md) — seats, roles, the organisation as the unit
- [`27-mobile-app.md`](27-mobile-app.md) — the same surface in a pocket, and the badge it still needs
