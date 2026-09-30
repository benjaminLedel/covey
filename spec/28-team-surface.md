# 28 — The team surface: the day's working view

**Status: the first slice is built** (#298 — the shell, the thread, the reply at the question, the search, reactions). Section 5 is specified and open as #302. Conversations with members (#440) and the inbox folded into them (#441) are decided; sections 2 and 3 describe them, and the code follows. This document describes what it becomes, and it exists because the next four steps all pull on the same joint: what the team surface is allowed to show.

The console is the view of somebody who *builds* a workforce. The team surface is the view of somebody who *works with* one, and that is a different question about scope at every turn — which is why it needs writing down once instead of being decided four times.

## What is built

A shell of its own at the root: the colleagues grouped by department, and a list of conversations — direct and group, newest first (section 3). A message to an agent is handed to it (section 5; with the triage off it becomes a task), a reply at a question becomes the resume input of the parked task, and what the backlog knows is read out of the backlog rather than copied into the conversation ([`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md), `internal/httpapi/chat.go`). Each agent carries a face computed from its slug, and the face shows whether it works, sleeps or has been stopped.

**It is an opt-in per organisation, off by default while it is in beta** (#328). `organizations.team_surface` holds the switch, `GET`/`PATCH /api/v1/org/team-surface` read and set it (reading: every role; setting: whoever manages the organisation), and `/auth/me` carries it as `TeamSurface` so the interface picks its shell on the first answer. Off means: the console keeps the root, `/team/<id>` leads to the agent's page, there is no switch between the shells, and posting into a conversation answers 403 (`POST /agents/{id}/messages` and the conversation endpoints alike). The refusal sits in the server, not only in the interface. Reading a conversation stays open to its members, and replying to a parked task and reacting stay open, because they act on tasks and messages that exist whether the surface is on or not. The triage switch (section 5) is shown only while the surface is on, since without it there is no message to decide about.

## 1. The org chart belongs here, as the directory

The console's org chart answers *how is this organisation built* — it is editable, it drags departments around, it assigns supervisors. That is administration, and it stays there.

Under the team the same structure answers a different question: **who do I talk to?** So it is not the same screen with the editing switched off; it is a directory that happens to be shaped like the org chart:

- read-only, no drag, no rename
- every node is a door: a click opens the direct conversation with that colleague, or creates it when there is none yet
- humans are in it and are not a second class — a person is opened the same way as an agent, and a direct conversation between two people is as ordinary as one with an agent (see 2)
- it is the picker: selecting several nodes is how a group conversation starts, and a group carries a title (see 3)

That last point is the reason to build it at all. A flat list of colleagues stops working at forty, and a search finds who you can name. The chart finds who you *cannot* name — "the person who reviews invoices in finance" is a position in a structure, not a string.

## 2. Humans in the conversation

A conversation has members, and a person is a member the way an agent is. What differs is how a message reaches them. An agent has a backlog and a wake; a message to it is handed to the agent, and the agent answers (section 5). A human has neither, so a message to a human reaches them in two ways, and they are not exclusive:

- **the conversation itself**, which is also where everything else that waits for this person now stands — the agents' questions, the approvals, the open points of a review (below)
- **a notification** — push to the app ([`27-mobile-app.md`](27-mobile-app.md)) or mail ([`06-observability-control.md`](06-observability-control.md)) — to the conversation's members, except the author and whoever muted it. The platform already has the mail plumbing (`internal/notify`).

**The conversation is the truth, the notification is the nudge.** The conversation holds the message and, where there is one, the decision; the notification reaches somebody who does not have the tab open and says only that something waits — it carries no content ([`27-mobile-app.md`](27-mobile-app.md)).

### Decidable entries: what the inbox used to hold (#441)

What waits for a person's decision was spread over two places: the conversation carried the agent's questions, and an inbox carried the approvals ([`06-observability-control.md`](06-observability-control.md)) and the open points of a review — proposals, findings, filed issues, tool requests ([`21-operations-and-improvement.md`](21-operations-and-improvement.md)). A person looked in two places for the same thing — *the agent needs me* — and the inbox showed it without the conversation it belonged to.

**Decided: they are decidable entries in the conversation of the responsible person.** The responsible person is the person the task came from; for a task that came from no person (a heartbeat, a webhook, a delegation, a review cycle) it is the agent's supervisor. The entry sits in the direct conversation between that person and the agent whose task raised it — for a task opened from a direct conversation, that is the one it came from; an entry is not put in front of a whole group — and is decided there, through the same decision endpoints with the same verbs and checks as before: approve / reject for an approval, accept / reject with a reason for an open point. Push announces it like a question. An open point of a review is the one case that needs a closer reading, and [`21-operations-and-improvement.md`](21-operations-and-improvement.md) gives it: the responsible person is the supervisor of the agent the point is about, and the conversation is theirs with covey Doctor, never with the reviewed agent.

**Rights do not move with the entry.** Who may decide an approval or accept a configuration change is unchanged — the manage roles or `security` for an approval, `org_admin`/`security` for a proposal that touches `ACCESS.md` or `EGRESS.md`. When the responsible person may not decide an entry, it still appears for them, says who decides, and offers no button; and it is delivered as well to the people who may — the agent's supervisor first when they hold the role, otherwise the holders of the role — each in their direct conversation with the agent. Deciding it anywhere closes it everywhere.

**Only a person decides.** An agent's own turn in a conversation — the triage, the retelling of a result — never approves, accepts or rejects an entry, whatever the conversation says to it (section 5).

**Gone:** the inbox page, its counter in the navigation, `GET /api/v1/inbox`, and the app's "What waits" list. The decision endpoints stay; the conversation calls them.

## 3. Conversations: direct and group

A thread with several participants is the point where "chat" stops being a view of one agent's backlog and becomes an object of its own. The per-agent thread was such a view, and it showed why the view is the wrong shape: one conversation per agent, shared by everybody in the organisation, carrying the result of **every** task of the agent — work from the backlog, a webhook, Jira or Zendesk that the people reading it never asked for — while each of them read what the others had written.

**Decided (#440): a conversation is an object, and it owns no work.** It holds members and messages; the backlog stays the ledger ([`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)).

- **Two kinds.** A *direct* conversation has exactly two members — a person and an agent, or two people — and there is one per pair. A *group* has several members, people and/or agents, and a title. Tables `conversations`, `conversation_members` (with the member's read position and whether they muted it) and `conversation_messages`.
- **Only members read and write.** Nobody reads a conversation because they may manage the agent in it. The roles `auditor` and `org_admin` read and export conversations in the audit ([`06-observability-control.md`](06-observability-control.md)) — a reading of the record, not a seat in the conversation.
- **When an agent speaks.** In a direct conversation it answers every message. In a group it answers only when it is addressed: mentioned by name (`@`), or replied to on a message of its own. That closes what used to be open here — the difference between a group chat and a swarm — on the side of the group chat: an agent in a group does not answer a message that was not addressed to it, whoever wrote it.
- **Beside the backlog, not a view on it.** When work comes up in a conversation, the agent looks into its own backlog — what is there, its state, its result — or opens a task linked to the conversation, and says in the conversation what matters. An agent that answers in a group reads the other members' messages as context, and nothing in the dispatcher changes.
- **Nothing from the backlog is mirrored in.** Two messages are the exception, both written by the platform for the agent: a task opened from a conversation reports its outcome back into that conversation (`backlog_tasks.conversation_id`, section 5), and a question a task parks on that came from outside any conversation goes into the direct conversation between the agent and its supervisor. The decidable entries of section 2 are the one further kind the platform writes, and they go to the responsible person.
- **The old threads are not carried over.** The per-agent threads (`chat_messages`) stay readable in the audit and nowhere else, and every conversation starts empty. Carrying them over would have meant assigning each line of a shared thread to members it was never written to. The per-agent thread endpoints remain as aliases onto the person's direct conversation with the agent, so an app built against them keeps working ([`27-mobile-app.md`](27-mobile-app.md)).

## 4. Several organisations at once

**The requirement:** somebody with seats in more than one organisation must see all of them in the team surface without switching. It is the daily working view; a person does not think in tenants at nine in the morning.

**Why that is not a frontend change.** Every read in this API is scoped to the *active* seat: `principalFrom(r).OrgID` decides what a query returns, and switching is `POST /auth/switch-org`, which rewrites the session. Asking twice with two sessions is not an option a browser has, and it would be the wrong answer anyway — the session is also what the audit trail records.

So the team surface needs **account-scoped reads**: a small set of endpoints that answer for every seat of the account instead of for one organisation. Three rules make that safe, and none of them may be skipped:

1. **The role is per seat, not per account.** Somebody may be `agent_owner` in one organisation and `auditor` in another. A cross-org answer applies each seat's own role to each seat's own rows — the active seat's role may never leak across the boundary. That is the whole reason this cannot be done by widening an existing handler with an `OR`.
2. **The organisation is visible on every row.** A colleague list mixing two companies without saying which is which is a mistake waiting to be made by the reader, not by the code.
3. **Writing stays single-org.** A message creates a task in exactly one organisation — the one the agent belongs to. Reading crosses the boundary; writing never does, and the existing `agentScoped` middleware keeps doing what it does.

What it costs: `GET /agents`, `GET /departments` and `GET /conversations` need account-scoped twins, and the audit trail has to record which seat a read came from. What it buys: the surface stops being a per-tenant console and becomes what the name says.

**Open:** whether the console follows. The recommendation is no — administration happens inside one organisation, and a fleet-wide admin view is a different product with a different threat model.

## 5. A message is not automatically a task

Without the triage every message to an agent becomes a backlog task. That is right for "check the
Globex invoice" and absurd for "did that go out yesterday?" — the second gets
an isolated sandbox, a runtime seat and a line in the cost list in order to
answer one sentence. It is also slow in the direction that matters: a
colleague answers in four seconds, ours opens a ticket.

The message should be handed to the agent, and **the agent decides what kind
of thing it is**: answer in the conversation, acknowledge with a mark, or open the
task. Whether "look at the invoice" is a question or a job depends on the
agent's role, and the agent is the thing that knows its role — so this is not
a rule in the interface.

**It needs an engine of its own, configured per organisation** (#302). Not
the agent's runtime: that is a sandbox plus a seat plus a wake, which is the
machinery the triage exists to avoid paying for. It is a cheap turn inside
the control plane, and covey already has that shape — `internal/httpapi/assist.go`
uses the organisation's credential server-side, without a sandbox, to help
write a config. Off is the default, and off means today's behaviour exactly.

The model is the organisation's control-plane credential, found the way a run finds its own (#483, [`18`](18-runtimes-capacity.md)): the two org secrets by name, else the first usable credential of the Claude Code seats. `GET /api/v1/org/chat-triage` answers `available: false` only when neither exists, and then the settings card and every conversation with an agent say so — what is missing, that every message becomes a task until then, and, for an administrator, the links to Secrets and Infrastructure — rather than showing a greyed-out switch. The default stays off even where the model is available: it was chosen so that an upgrade changes nothing and no turn is paid for that nobody switched on, not because a credential was missing, and `organizations.chat_triage` cannot tell a switch left alone from one turned off.

The limits are the point, not a detail, because the turn runs **outside the
sandbox**:

- no target systems, no credentials, no sandbox — no ticketing, no repository,
  no mailbox, no file, no command. Everything it would need beyond that is the
  reason to open a task instead
- **but its own backlog, read and write.** That is the other side of the same
  line, not an exception to it: the backlog is covey's own object, reading it
  needs no credential and leaves nothing, and without it the agent can only
  guess at "what are you working on?". So there are three moves, not two —
  answer, write onto a task that already exists, or open a new one. A note
  stands at the task and is not seen in the conversation, so it carries a
  reply that is posted there (#460), and a question about where a task
  stands is an answer, not a note. The short
  id a note refers to is one the turn was shown; what the model says never
  becomes an identifier pointing at something it was not handed
- **and the org chart, read only** (#415) — the same team of humans and
  directory of AI colleagues a run gets at dispatch, from the same function,
  so the chat and the run never describe a different organisation. "Do you
  know the QA agent?" is a question to answer, not a task to open
- it sees the end of the conversation, not all of it, and **looks up** what
  lies further back (#416): a fourth move, `search`, has covey search the
  whole of the conversation it answers in — not the agent's other
  conversations — and the org chart — a name with a typo included — and
  asks once more with the hits. The org chart it reads carries stopped
  colleagues too, marked; a run's directory does not
- a turn that fails is tried once more; when it still fails, the message
  becomes a task as before and the reason is noted at that task
- its output is text or a task, nothing else: no config change, no wake, no
  approval or other decidable entry decided — only a person decides one
  (section 2). The one addition, behind a trial setting, is a config
  *proposal* (below): stored, not in effect, decided by a person
- it is recorded and counted like any other run ([`06`](06-observability-control.md))
- the guard rails on what an agent says hold for a direct answer exactly as
  they hold for a ticket reply — a cheap path must not become the cheap way
  around them

**How it talks** (#411). A conversation is only a conversation if the other
side talks like a colleague, and three things kept it from that: a task
started in silence, its result arrived as the run's report — written for the
record, with headings and lists — and the triage knew the agent's title but
not its voice. So:

- the triage's decision to open a task carries a short acknowledgement in the
  agent's voice, written into the conversation at once ("sure, I'll look at the
  invoice") — promising nothing about the outcome;
- when a task opened from a conversation is done or has failed, a second
  cheap turn tells the members what came out, in a few sentences of chat,
  from the result and the conversation alone. It is kept on the task and
  written into the conversation the task was opened from
  (`backlog_tasks.conversation_id`), shown in the list of conversations and
  in the push notification to the members except the author and whoever
  muted it (which waits for it); **the report stays one tap away**, because the sentence is a
  retelling and nobody should have to take it on trust;
- both turns get the agent's SOUL.md, and ask for chat as a colleague writes
  it rather than for a sentence count.

The second turn stands under the same limits as the first — no target
system, no credential, nothing claimed beyond what the result says. Without
a model it says nothing, and the report stands on its own as before.

**Without the triage** (#483) a message is still answered, not reported on. It becomes a task, because nothing decided otherwise — but a task marked as the answer to that message (`backlog_tasks.chat_answer`, set in the same insert and carried on by a continuation). Its run is told so at dispatch (`agents.ChatAnswerDoc` in place of the retelling instruction, plus the chat tone the triage would read): sort the message first, answer a greeting or a question without touching a target system, and write the result as the chat reply itself — short, in the message's language, no headings, no account of the run. When it is done, that result is written into the conversation as the agent's plain message (kind `text`, `meta.reports = result`, so the round writes it once), not as a `result` entry with the report behind it; a failure stays an `error`. A task the triage opened is not a chat answer and keeps the retelling. A brief to the People department is work whatever the switch says and keeps its result entry, which carries the drafts.

**As a teammate** (#457). A greeting in a group came back as "I answered your greeting and explained my role" — a report about an answer instead of the answer. Three changes, one per place the wording came from:

- the triage's reply is always the JSON object, but a short chat line without any JSON is read as the answer rather than as a failed turn, so a greeting that the model answered with a bare emoji no longer becomes a task;
- a task opened from a conversation is told at dispatch that its result is read out to the people there, so it carries the content itself; every other task keeps the summary for the record;
- a request to act in a target system is a task even when the ticket is unknown; "I cannot see it" answers only a question about what already happened;
- both turns are told that in a group the agent is one colleague among several: the author is named and addressed by first name, a greeting is answered as a greeting, nothing is said about tasks, runs or reports, nothing repeated that somebody already said, and less said than in a direct chat. A result that only records that something was done is not retold as such.

How the agent talks there comes from the voice chosen for the chat — the department of the person who wrote, else the agent's chat slot, else the organisation's — with its card, two passages and its chat tone (address, tone, emoji and a line of free text, with an organisation default), plus the "how to speak with us" lines of the departments involved; the answer's meta names the voice and why ([`24`](24-voice.md), voices per occasion). An evaluation set of conversation scenarios with hard checks and a model-graded score (`internal/chat/testdata/eval`, `make eval-chat`) measures a change to these prompts.

**A wish to change the agent itself** (#491, a trial). "Check the inbox only every two hours" is neither a question nor work a run could do: an ordinary agent may not touch its own configuration (`covey/propose_agent_config` needs `agents:write` or `agents:review`). Behind the organisation setting `chat_config_proposals` (`GET`/`PATCH /api/v1/org/chat-config-proposals`, `{"enabled": bool}`, off by default, the manage roles switch it beside the triage) the triage gets a fifth move:

- `{"action":"config","title":…,"change":…,"text":…}` when the message asks the addressed agent to change its **own** setup — heartbeat, playbooks, soul, access. A question about the setup is an answer (the turn sees the agent's `HEARTBEAT.md` for that), a one-off job is a task, and a colleague's setup is the colleague's. In a group only the addressed agent's triage can choose it. With the setting off the move is not offered, and a model that chooses it anyway gets a task, so the wish is not lost;
- the server drafts the change with the config assistant's turn (`assistDraft` in `internal/httpapi/assist.go`, the same one `POST /agents/{id}/config/assist` takes, through `llm.Resolve`) from the running config files and the change in plain words, keeps only the files that really change, runs the checks the accept path would refuse on (`HEARTBEAT.md` must parse; a proposal never grants the system `covey`), and stores it as the same kind of row `propose_agent_config` writes — a proposal in `improvement_items`, not in effect — with its origin: `origin_message_id` and `requested_by` (migration 0124);
- the conversation gets a message of kind `config_proposal` (`meta.proposal_id`; its text is the title and the rationale, for a surface without the card) and a short line in the agent's voice, in which covey puts the names of those who may accept where the turn wrote `{approvers}`. A draft that fails is answered plainly in the conversation, never turned into a task a run could not do either.

The card is read with the conversation (`proposal` on the message and on the thread entry): the diff per file against the running state, the rationale, which of `ACCESS.md`/`EGRESS.md` it widens, whether the reader may decide (`can_decide`, `can_accept` — the server's answer, not a role table in the client), whom it waits for, and once decided, who and when. It is decided through `POST /api/v1/improvements/{id}/decide`, the agent page's path, so accepting from the chat is accepting there: a new version with the person as author, the lint, the write-through of `ACCESS.md`/`EGRESS.md`, org admin or security for access, a conflict refused. A decision anywhere moves the conversation the proposal came from over the event stream. The app shows the text with a label that sends the reader to the web.

What the trial does not do yet: the voice slots are not a config file and are not drafted; a proposal that is superseded by a later one is not withdrawn; the drafting turn's cost is not attributed to the agent.

**Said in a call** (#502). A message the app posts from a call (`meta.via = "call"`, #494) is answered in two forms: the written one the conversation shows, unchanged, and a spoken one the call reads out — one to three short sentences, no ids, links, lists, emojis, markdown or parentheses unless asked for ("the merge request for the login bug", not "MR !475"), numbers as they are said, in the same voice. It stands in the agent message's meta: `spoken`, and `details_in_chat = "true"` when it left something out, so that the call adds that the details are in the chat. Three places write it, and a typed message gets none — neither the prompt nor the meta changes for it:

- the triage, asked only for a message said in a call: `"spoken"` and `"details_in_chat"` beside the move, for an answer, a note's reply, a task's acknowledgement and the line beside a config proposal (with the approvers put in there too);
- the run of a chat answer (#483): the task is marked `backlog_tasks.said_in_call` in the same insert (migration 0127, carried on by a continuation), its run is told to end its reply with `<spoken details_in_chat="…">…</spoken>` (`agents.ChatAnswerCallDoc`), and covey takes the tag off the reply it posts;
- any other report of a task from a call — the retelling, the report itself, an error: one short turn on the fast tier (`chat.Sprechfassung`) when it is posted. Without a model, or when that turn fails, the message has no spoken form and the call speaks the written one.

The spoken form is bounded (`chat.SpokenMax`); a parked question from a call is still spoken as written.

The spoken form is in the written answer's language (#511). Each of the three prompts says so, and names the language when it is known — the triage the conversation's (`normalise.Language` over the message, else the last lines of the conversation), the turn after a result the message's; a German conversation gets a German example. A spoken form that `normalise.Language` reads as another language than its written answer (or, when the written answer is too short to tell, than the conversation) is dropped (`chat.SpokenFits`), and the call speaks the written answer. The detector is sure or says nothing: it counts function words that belong to one of the app's Latin-script languages only and tells Japanese and Chinese by their script; a text too short to tell keeps its spoken form.

The open decisions are in the issue; the load-bearing one is what the answer
is allowed to know. The recommendation is: the role and the conversation, not the
wiki memory. An answer that needs the memory is an answer that should have
been a task.

## What the surface must not become

A second console. Every one of the four steps above has a version that ends there: an editable chart, a message that can change a config, a group conversation that assigns work to departments, a cross-org view that also writes. The line is the same each time — **the team surface reads across and writes narrowly**, and anything that configures an agent stays where the config already is. The config proposal of section 5 keeps to it: a message produces a proposal, and putting it in effect is the agent page's accept, with its roles and checks.

## Related

- [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md) — the backlog the conversations co-exist with
- [`06-observability-control.md`](06-observability-control.md) — approvals, decided in the responsible person's conversation; the recording; conversations read and exported in the audit
- [`09-enterprise-model.md`](09-enterprise-model.md) — seats, roles, the organisation as the unit
- [`27-mobile-app.md`](27-mobile-app.md) — the same surface in a pocket, and the badge it still needs
