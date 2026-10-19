# Workflows — the path work takes between colleagues

> Status: **partly built.** The engine, its storage, the hand-over to agents,
> the API and the interface exist (#561–#565); triggers and actions reuse
> covey's existing mechanisms (#566). §14 says what is built and what is next;
> §15 lists the open questions.

Agents rarely finish a piece of work alone. A change passes from the developer
to a code review, from there to acceptance, back to the developer with defects,
and on to the person who ships it. A support ticket passes from triage to a
specialist and back for the reply; an article from the writer to the editor to
publication. Software is the first case, not the only one: a workflow is about
hand-overs in whatever target system carries the work.

This document makes that path an object of the organisation: described once,
run by covey, shown while cases move along it.

## 1. What goes wrong without it

Today the path exists only as prose in the playbooks of the agents who walk it.
That breaks in five ways:

- **Names instead of roles.** A developer's playbook says "set `reviewer-a` as
  reviewer". When `reviewer-a` is stopped, the developer keeps naming it. The
  run's directory leaves stopped colleagues out (#416) and #556 adds a warning to
  the prompt — symptoms of a path that names people.
- **One path, several versions.** The same relay stands in two or three configs,
  each from the angle of its station, each edited at a different time.
- **Rules of a project live with one agent.** "In this project we assign, we
  never comment" is a paragraph in one agent's `SOUL.md`; the next agent does not
  know it.
- **Nobody sees what lies still.** A merge request that has sat with a station for
  five days is visible to nobody until the customer asks.
- **Hand-overs are implicit.** Which field means "your turn" differs per system,
  and each playbook says it in its own words.

All five have one cause: the path is nowhere. Every agent reconstructs it from
its own instructions on every run.

## 2. What a workflow is

A **workflow** is a definition of the steps a case goes through, who does each
step, and where it goes next. It is an **object of the organisation**, beside the
org chart — not of an agent, and not of a repository: the paths run through
ticket systems, mailboxes and CMSs as much as through GitLab.

- **A case, a run.** Each case — a ticket, a mail thread, a request — that enters
  a workflow gets one **run**. The run knows the step the case is at, who has
  it, what each step reported, how many rounds a loop took, and its history.
  The target system still holds the work itself (the ticket, the merge request);
  covey holds where the case stands in the workflow.
- **Roles, not names.** A step names a role. Who fills it is decided when the step
  starts (§5).
- **Versioned like a config.** Every save is a version, kept as the text it was
  written in ([`02`](02-agent-model.md)). The canvas in the interface and the text
  are two views of the same definition.
- **Three modes.** *Off*: the definition exists and does nothing. *Observe*: covey
  records where a case stands and shows it, without assigning or acting — the
  agents hand over by their playbooks. *Steer*: covey starts the steps, picks the
  colleague, hands over and acts.
- **Optional.** Agents that stand in no workflow work as before: heartbeat, chat,
  backlog. A workflow belongs where several roles work on one case in turn.

What a workflow is **not**:

- **Not a replacement for playbooks.** How a colleague does a step stays in its
  playbook and skills. The workflow decides who comes next.
- **Not a second set of integrations.** A workflow starts from the triggers covey
  already has and acts through the target-system actions agents already use,
  under the same access, guard rails, approvals and audit (§7, §8).
- **Not a general-purpose programming language.** No code steps, no loops that
  are not counted: a definition that does not fit the building blocks is a sign
  it should be two workflows, or a skill.

## 3. The definition

One definition per workflow, YAML, stored by covey (`workflows`,
`workflow_versions`). The keys are the file format's and stay English:

```yaml
workflow: "Customer ticket"
owner: "alex"            # gets what the workflow cannot resolve itself
mode: steer               # off | watch | steer

roles:
  "Engineering": [builder, kim]   # agent slugs
  "Test": [tester]

steps:
  t:
    type: trigger
    title: "Ticket assigned"
    as: "builder"                  # whose access a gate uses (§7)
    every: "15m"
    only_if: "gitlab:issues:assignee:builder:acme/timetable"
    webhook: "zammad"
    token: true
    next: b
  b:
    type: agent
    title: "Build"
    role: "Engineering"
    sticky: true                   # another round goes to the same person
    max_parallel: 2                # per colleague; beyond it, cases queue
    task: "Implement {{case.title}}. Earlier defects: {{a.defects}}"
    result: [mr]                   # fields the colleague reports (§9)
    limit: "4h"
    next: a
  a:
    type: agent
    title: "Acceptance"
    role: "Test"
    not_same_as: b                 # four eyes
    task: "Test {{b.mr}}. Report result: accepted or defects."
    result: [result, defects]
    next: c
  c:
    type: condition
    title: "Accepted?"
    on: "a.result"
    max_rounds: 3
    next:
      "accepted": s
      "defects": b                 # the way back counts a round
      "limit": h                   # rounds used up
  s:
    type: action
    title: "Ticket to Alex"
    as: "builder"                  # the colleague the action runs as (§8)
    system: "gitlab"
    do: "assign"
    params: { project_id: "{{case.project}}", issue_iid: "{{case.iid}}", assignee: "alex" }
    next: w
  h:
    type: approval
    title: "What next?"
    who: "alex"
    ask: "Three rounds without acceptance on {{case.no}}. Last defects: {{a.defects}}"
    next:
      "retry": b
      "stop": e
  w:
    type: wait
    title: "Customer test"
    until: "label:customer-ok"
    max: "14d"
    next:
      out: e
      timeout: e
  e:
    type: end
    title: "Done"

layout:                            # positions on the canvas; the engine ignores it
  t: [40, 80]
```

`{{step.field}}` is filled from what a step reported; `{{case.…}}` from what the
trigger brought (§7). A placeholder with nothing behind it stays visible rather
than becoming a gap in a sentence.

**Checks before saving.** A definition that cannot run is not saved, and the
author gets the reasons: no trigger or more than one, an exit that leads to no
step or nowhere, a step nothing leads to, a loop that no condition counts or
that has no limit exit, a four-eyes rule that names a step without a colleague,
an approval without a person, a wait without an event or a maximum. The same
check runs while the author types.

## 4. Building blocks

| Block | What it does | Exits |
|---|---|---|
| trigger | where cases come from (§7) | one |
| agent | a colleague from a role does the step as a backlog task (§9) | one |
| ai | a short model call without a colleague — classify, summarise | one |
| condition | branches on a reported field; counts the rounds of a loop | one per value, `else`, `limit` |
| action | covey does something in a target system, as a colleague (§8) | one |
| approval | a person decides | one per option, `timeout` |
| wait | until an event arrives, or time passes | one, `timeout` |
| notify | a message to a person | one |
| end | the run is finished | — |

## 5. Roles

A role is a pool of colleagues (agent slugs). When a colleague step starts:

- **Who may take it**: the role's members that are hired and not stopped
  ([`06`](06-observability-control.md)), minus the person who did the step named
  by `not_same_as`.
- **Who had it before**: with `sticky`, another round goes to the person who did
  the step last time, if they still can — they know the branch and the defects.
- **Pick**: whoever has the fewest cases in work (`load`, the default), or in turn
  (`round`).
- **Limit**: with `max_parallel`, a colleague at the limit takes no more; the case
  **queues** and goes out oldest first when someone is free. A role that queues
  for long is understaffed, and the live view says so.
- **Nobody**: an `optional` step is skipped. A required one waits and the owner
  hears it once; when someone becomes available, the step starts.

## 6. Runs

A run is in one of: *working* (a colleague or the model has the step), *queued*,
*waiting* (for a person, an event or time), *watching* (observe mode), *done*,
*failed*. Every change is written to the database in one transaction with the
others of the same call; a restarted control plane rebuilds its runs and carries
on. A case that comes back — a new comment, a second webhook — continues its open
run; the database refuses a second open run for the same case.

The clock (every 30 seconds) reports a colleague step over its `limit` once, ends
a wait past its `max` by its timeout exit (or its only exit, for a plain delay),
ends an approval past its `wait` by its timeout exit, and retries required steps
that found nobody.

A loop is counted by the condition that sends the case back: with `max_rounds: 3`
the case goes back twice; the third time the condition takes its `limit` exit.

A person who manages agents can **end a run by hand** — a case started by
mistake, one the customer withdrew, one that should never have started. The run
stops where it stands and ends *failed* (it did not reach an end; there is no
state of its own for this), its log says who ended it and why, and its case is
free again. A run at a colleague step has put a task into that colleague's
backlog; it is cancelled with the run, so the agent does not go on working for
a run that is over.

## 7. Triggers — the ones covey already has

A workflow starts from the sources that wake agents today. They are declared on
the trigger step, in the same terms:

| Trigger | Agent today | On a workflow | The case is |
|---|---|---|---|
| Schedule | `alle:` / `täglich:` in `HEARTBEAT.md` | `every:` / `daily:` | one per firing |
| Gate | `nur-wenn: <system>:<kind>` | `only_if: <system>:<kind>` — same gate code, same signature dedup | one per waiting item, `<system>:<key>`, when the plugin signs items; else one per firing with new work |
| Target webhook | `/api/webhooks/<system>/<agent>` | `webhook: <system>` → `/api/webhooks/<system>/workflows/<id>` | the event's correlation key (or its dedup key) |
| Token trigger | `/api/trigger/<token>` | `token: true` → `/api/trigger/workflows/<token>` | `case`, else `dedup_key`, else a new key |
| By hand | — | the interface, `POST /api/v1/workflows/<slug>/runs` | as given |

- **Same code, not a copy.** The gate is the heartbeat's gate evaluator: the
  plugin's work check, its credential, its fail-open on a broken check, its
  signature — a gate that sees the same work as last time does not fire again.
  A webhook is parsed and verified by the target system's own webhook code and
  deduplicated in `webhook_events`.
- **Whose access.** A gate needs credentials. The trigger names the colleague it
  checks as (`as:`), and the gate resolves that colleague's secrets exactly as
  for its heartbeat. A webhook needs none beyond the system's webhook secret.
- **A known case.** An event whose case has an open run is delivered to the run
  as an event named `webhook:<system>`; a `wait` step can wait for it. It does
  not start a second run.
- **Items, not just "work".** A gate's signature is, for the plugins that sign
  their work, already one `<key>@<version>` entry per waiting item — GitLab's
  `issue12!4@881`, Jira's `OPS-7@<updated>`. The plugin SDK writes that shape
  down (`target.WorkSignature`, `target.WorkEntries`), and a plugin says once
  that its signature has it (`target.ItemSigner`; a manifest plugin by giving
  its poll a `key_field`). It has to be said, not guessed: a signature hashed to
  one entry looks like one item. Nothing in it is about workflows — the same
  signature stays the heartbeat's watermark, byte for byte.

  When the gate's plugin signs items, a firing compares them with the last
  firing's. Each new or changed item is a case `<system>:<key>`:
  - with an open run, the change is delivered to the run as the event
    `only_if:<system>`, which a `wait` step can wait for;
  - without one, a run starts, titled `<trigger title> · <key>`;
  - a case whose run **ended since the last firing** is not started again: the
    run's own last comment moved the item, and that is not news. This is the
    heartbeat watermark's call, with its blind spot — a foreign comment in the
    same interval is taken along.

  A plugin that does not sign items keeps one case per firing, and its first
  colleague works through what is there.

  A signing gate whose check fails answers as the heartbeat's does — work,
  without a signature. That names no item, so the firing starts nothing: a run
  without a case would hand its first colleague no ticket. The trigger keeps
  its last signature, and the next check that succeeds is compared with it.
- **Modes.** A workflow that is off starts nothing. In observe mode a trigger
  starts runs that record positions and do nothing else.

## 8. Actions — as a colleague

An action step runs a target-system action the way an agent's action proxy runs
it: the plugin's own action (`do:`) with its own parameters (`params:`), so
everything a plugin can do for an agent it can do for a workflow, and nothing
else. It runs **as a colleague** — `as:` names an agent or a role (the first
available member) — and so under that colleague's rules:

- the credential is brokered for that colleague: its `ACCESS.md`, the plugin's
  activation, the system's guard-rail deny;
- the colleague's tool assignment applies;
- the guard rails decide on the action's subject (`gitlab:assign`, …): a deny
  stops the run with the rule's name; a rule that requires approval creates the
  same approval an agent's action would, and the run **waits** at the step until
  it is decided — it goes on once it is granted (and the approval is used), and
  stops when it is refused;
- the action is recorded in the colleague's audit, marked as done for the
  workflow.

A workflow therefore has no identity of its own in a target system and no rights
an agent could not be given. Parameter values may carry placeholders; a value
that reads as a number or as true/false is passed as one.

## 9. Colleagues in a workflow

A colleague step becomes a backlog task for the picked agent. Its origin names
the workflow, run and step; its text is the step's task with the placeholders
filled, and — when the step asks for fields — how to report them.

The agent reports fields with **`covey/report`** (`{"result":"defects","defects":"…"}`):
flat values only, stored on the task, taken only for the task the run works on.
When the task ends, the run moves on with what the task and its continuations
(after a turn limit) reported. A task that fails — and was not continued — or ends
without the fields its step asks for stops the run, which says why. A late answer
for a step the run has left is ignored.

## 10. People

An **approval** names its person (e-mail, the part before the @, or display
name). That person decides, and so may whoever manages agents; nobody else.
Until the chat carries workflows, the question stands where the run waits — in
the workflow's views and in "waits for you" on the overview — and is answered
there (`POST …/runs/<id>/decide`). A **notify** step is not delivered yet; the
run goes on and its history keeps the message.

The chat is the place for both (#440, #441): a card in the person's chat
for an approval, a message for a notice — sent by the workflow, not by an agent.
Conversations today know only people and agents as members; a workflow as a
sender needs that schema extended (§15).

## 11. Storage and API

| Table | Holds |
|---|---|
| `workflows` | one per organisation and slug, pointing at its current version |
| `workflow_versions` | the definition as written, who saved it, when |
| `workflow_runs` | position, state, assignee, reports, rounds, who did which step, history |
| `workflow_triggers` | per workflow: when a schedule last fired, the last gate signature, the token |
| `backlog_tasks.report` | what an agent reported for its task |

Routes under `/api/v1/workflows`: list with counts; create; one workflow as
text and steps; check without saving; save the next version (refused with its
problems);
runs, one run; start by hand; decide; deliver an event; end a run; the approvals that wait
for the caller; the token; delete. Reading is open to everyone in the
organisation; writing and starting to those who manage agents.

A workflow is addressed by its id or its slug: every route under
`/api/v1/workflows/{…}` takes either, and the interface uses the id
(`/workflows/<id>`). The slug is the workflow's key in its file and stays
readable in scripts; it is not the address. **Creating** is its own call
(`POST /api/v1/workflows {source, slug}`): the slug is a suggestion, a taken
one gets the next free number (`release-2`), and a create never becomes a
version of a workflow that exists.

**Deleting** removes the workflow with its versions, runs and trigger state. It
is refused while the workflow watches or steers — it is switched off first, so
that nothing starts while it goes — and while a run stands at a colleague step,
whose task sits in an agent's backlog and would report to nothing. Runs that
only watch, queue or wait go with it.

## 12. Interface

**Workflows** is a place of its own in the rail, not a setting: the live view is
daily work, and a workflow crosses departments. The overview lists workflows by
what needs someone, with the approvals that wait for the person looking. The
editor is a canvas of blocks with a details panel that opens on a block, a live
view (cases per step: in work, queued, waiting, stuck), the list of runs with
filters and each run's path and history, and the text. The definition the
canvas writes is the text the engine reads; a fixture written by the interface
and parsed by the engine's tests keeps them together.

## 13. What stays the same

Playbooks, skills, the backlog, agents' own heartbeats and their webhooks, the
guard rails and the approvals. A workflow is added where hand-overs break, not
put over everything. One overlap needs care: in steer mode a case with an open
run belongs to the workflow, and an agent's own heartbeat that sees the same
ticket should leave it alone (§15).

## 14. Built, and next

| | |
|---|---|
| #561 | the engine: definition, checks, runs, roles as pools, loops, limits, modes |
| #562 | definitions and runs in Postgres; restore after a restart |
| #563 | steps as agent tasks; `covey/report` |
| #564 | the API |
| #560, #565 | the interface, on real data |
| #566 | triggers and actions reuse covey's (§7, §8); this document |
| #567 | one case per item a gate sees (§7); SDK `WorkSignature`/`ItemSigner`, GitLab, manifest `key_field` |

Next, in order:

1. **The chat.** Workflows as senders of approvals and notices.
2. **Agents' heartbeats stepping back** for cases a steering workflow holds.
3. **AI steps** through the organisation's model connection.

## 15. Open questions

- **A workflow in the chat.** Conversation members are people and agents; a
  workflow as a member, or messages with a system author in the owner's
  conversation with covey?
- **Heartbeat overlap.** How an agent's gate learns that a ticket is held by a
  workflow: a filter on the gate, or a rule in the prompt — the first is
  enforced, the second only told.
- **Per-item keys across systems.** The case key of a gate item and the
  correlation key of the same system's webhook should be the same string, so a
  case started by polling continues when the webhook arrives. Today they are
  not (`gitlab:issue12!4` against the webhook's own key), and a wait for
  either source names it (`only_if:<system>`, `webhook:<system>`).
- **More signing plugins.** Zammad prefixes its signature with the kind, and
  Zendesk hashes it; both need to move to `WorkSignature` before they can say
  `ItemSigner` — which changes their watermark once, and wakes their agents
  once more. GitHub, Jira and Salesforce already write the shape and only need
  to say so; the pack's manifests need a `key_field`.
- **Versions under running cases.** A new version takes over the open runs when
  their steps still exist; otherwise the old version keeps them. Should open runs
  be migrated by hand?
