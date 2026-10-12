# Workflows — hand-overs between stations as an organisation object

> Status: **draft**. Nothing of this is built. The document describes the
> problem, the shape of a solution and a thin first slice; the open questions at
> the end are open.

Agents rarely finish a piece of work alone. A change passes from the developer
to a code review, from there to acceptance, from acceptance to the merge and
back to whoever asked for it. Today that path exists only as prose in the
playbooks of the agents who walk it. This document makes it an object of the
organisation: described once, resolved by the control plane, told to each agent
as its own part, watched while work moves along it.

## 1. What goes wrong today

A relay between agents works, and it works the way [`03`](03-lifecycle-scheduling.md)
describes: the target system carries the baton (the reviewer field of a merge
request, the assignee of an issue), a heartbeat gate wakes whoever holds it, the
depth counter brakes a ping-pong. What it rests on is the playbooks — and that
is where it breaks.

- **Names instead of roles.** A developer's playbook says "open the merge request
  with `reviewer-a` as reviewer". When `reviewer-a` is stopped (kill switch,
  [`06`](06-observability-control.md)), the developer keeps writing that name
  into the reviewer field. The run's directory leaves stopped colleagues out
  (#416), but a silence loses against a config that names them; #556 adds an
  explicit warning to the prompt, which treats the symptom.
- **One path, several versions.** The same relay stands in two or three configs,
  each from the angle of its station, and each edited at a different time. The
  developer's playbook says the reviewer comes first; its wiki says acceptance
  comes first; the QA agent's playbook says it sends findings back to a station
  the developer's playbook no longer mentions.
- **Rules of a project live with one agent.** "In the customer's ticket project
  we assign, we never comment" is a property of the project. Today it is a
  paragraph in one agent's `SOUL.md`; the next agent that touches the project
  does not know it, and nothing enforces it.
- **Nobody sees what lies still.** A merge request that has sat with a station
  for five days is visible to nobody: the station does not run, the station
  before it has handed over and is done, and the human finds out from the
  customer.
- **The baton is implicit.** Which field means "your turn" differs per hand-over
  (reviewer field for a merge request, assignee for an issue, a label for a
  ticket system without either). Each playbook says it in its own words, and a
  heartbeat gate has to be written to match by hand.

All five have the same cause: the path is not anywhere. It is reconstructed by
every agent from its own instructions on every run.

## 2. What a workflow is — and what it is not

A **workflow** is a declarative description of a path work takes between
stations, kept by the organisation and versioned like an agent's config
([`02`](02-agent-model.md), config as code). It names:

- its **scope** — the target-system projects whose items move along it;
- its **stations** — roles, not agents;
- for each station, **what marks an item as being there** — the baton in the
  target system;
- the **transitions** — which station hands to which, on what outcome;
- the **rules of the scope** — what may and may not be done in which project;
- **dwell limits** — how long an item may lie at a station before a person is told.

What a workflow is **not**:

- **Not a second state machine.** The target system stays the truth. Where an
  item is, is read from the target system (who is in the reviewer field), never
  kept beside it. A workflow that disagrees with GitLab is wrong, not GitLab.
- **Not an actor.** In the first slices the control plane does not move items:
  it does not set reviewers, does not reassign. Agents act, as today. The
  control plane resolves, tells, watches and enforces rules.
- **Not a BPM engine.** No gateways, no timers that run code, no visual modelling
  language. The shape is a short list of stations and transitions, readable in
  one screen; a path that does not fit that shape is a sign it should be two
  workflows.
- **Not a replacement for playbooks.** How a station does its work (how to review,
  how to accept) stays in the agent's playbook and skills. The workflow takes
  over the parts that are about *other* stations: who they are, how to hand over,
  what is allowed where.

## 3. The file

One file per workflow, `workflows/<slug>.md`, held by the organisation (not by
an agent), edited in the interface or imported, every save a version. A small,
line-based syntax in the spirit of `HEARTBEAT.md`; the keys are English, since
this is a new format and a parser reads it.

```
# Workflow: customer change

scope:
- gitlab: customer/tickets   as: tickets    # the customer's issues
- gitlab: vendor/app         as: code       # code and merge requests

stations:
- build        role: developer    baton: code.mr.assignee
- review       role: code-review  baton: code.mr.reviewer   optional
- acceptance   role: qa           baton: code.mr.reviewer
- decision     human: supervisor  baton: tickets.issue.assignee

flow:
- build      -> review       on: mr opened, findings addressed
- review     -> acceptance   on: approved
- review     -> build        on: findings
- acceptance -> decision     on: merged
- acceptance -> build        on: findings
- decision   -> build        on: reopened

rounds: build <-> review 3, build <-> acceptance 3   # then a human

rules:
- tickets: read, assign            # nothing else — no comment, label, state
- code: read, write, comment, merge

dwell:
- review 1d, acceptance 1d, decision 3d -> notify owner

owner: <person>                    # who is told when something lies still
```

Notes on the shape:

- **`as:`** gives a project a local name so that batons and rules do not repeat
  paths. A scope may span projects — the common case is tickets in one project
  and code in another.
- **`baton:`** is `<scope>.<item>.<field>`. The first vocabulary is small:
  `mr.reviewer`, `mr.assignee`, `issue.assignee`, `label:<name>`. A target-system
  plugin declares which batons it can read, in its `Descriptor`
  (covey-plugin-sdk) — a workflow that names a baton its plugin cannot read is
  refused at save time, not at run time.
- **`optional`** marks a station that is skipped when nobody fills it (below).
  A station that is not optional and not filled stops the path at the owner.
- **`human:`** makes a station a person. `supervisor` resolves to the supervisor
  of the agent handing over; an e-mail address or display name to that person.
- **`on:`** names outcomes in words the handing agent uses in its result and its
  comments; they are documentation for the agent and labels for the view, not
  events the control plane parses in the first slices.
- **`rounds:`** moves the "three rounds, then a human" rule from every playbook
  into the path, and gives the per-station depth counter of
  [`03`](03-lifecycle-scheduling.md) a limit per pair of stations.

## 4. Roles and their resolution

A station names a **role**. An agent fills a role in one of two ways, the first
wins:

1. **Explicitly**, in the workflow's assignment table (interface, not file):
   `review: reviewer-a, reviewer-b` — an ordered list of candidates.
2. **By responsibility**, when the table names nobody: agents whose
   `responsibilities` carry the role's name, preferring the handing agent's own
   department — the rule the colleague directory already states in prose
   ([`02`](02-agent-model.md)).

**Resolution happens at dispatch** — at the moment a run starts, for every
station that run may hand to. A candidate counts when it is hired, not stopped
(neither its own kill switch nor the fleet's), and its runtime is not exhausted
([`18`](18-runtimes-capacity.md)). The first candidate that counts fills the
station. When none does:

- an **optional** station is skipped — the transition goes to the station after
  it, as if the skipped one had approved;
- otherwise the station resolves to the **owner** of the workflow, a person,
  and the hand-over becomes a message ([`28`](28-team-surface.md)) instead of a
  field in the target system.

The result of a resolution is concrete: the agent, and its identifier in the
target system of the baton (`gitlab: reviewer.a`). That is what the prompt says
— never the role alone, because "the code review" is exactly the kind of name an
agent guesses a username for.

This replaces the warning of #556 for every agent that sits in a workflow: a
stopped colleague is no longer something the agent is told to avoid, it is
something it never hears of. #556 stays for the agents outside a workflow.

## 5. What the agent sees

At dispatch, an agent that fills a station in a workflow whose scope the task
touches gets a section in its prompt — the same place the team directory goes
([`02`](02-agent-model.md)):

```
## Workflow "customer change" — you are: build

Items reach you when you are the assignee of a merge request in vendor/app.
When you are done:
- opened or findings addressed → set the reviewer to  reviewer.a  (review, Reviewer A)
Rounds with review so far on this item: counted by covey, the limit is 3.

Rules of this workflow:
- customer/tickets: read, assign. Never comment, label, change the state or close.
- vendor/app: read, write, comment, merge.
```

The section names the resolved identifiers, the transitions *out of* this
station and the rules of the scope; it does not repeat the whole path. A
station that is skipped today is not mentioned — "the review is skipped" is the
resolution's business, not the agent's.

Two things follow for configs:

- **Playbooks lose names.** "Hand to the code review" becomes "hand on as your
  workflow says". The bundled agent templates and the `covey-agent` skill write
  that sentence instead of a slug.
- **One agent, several workflows.** An agent may fill stations in more than one
  workflow; it gets one section per workflow whose scope its task touches, and
  none for the others. A task that touches no scope gets no section.

## 6. Waking: batons become gates

A baton is also the answer to "when does this station have work": the agent
holding the baton is the one who should wake. So a station derives its gate:

| Baton | Gate |
|---|---|
| `code.mr.reviewer` | `gitlab:review` restricted to the scope's code project |
| `code.mr.assignee` | `gitlab:mr` restricted likewise |
| `tickets.issue.assignee` | `gitlab:issues:assignee:<self>:<project>` (#546) |
| `label:<name>` | a plugin-declared label gate |

Derived gates appear in the agent's heartbeat tab with the source `workflow`,
next to the `config` and `system` sources that exist today; the interval comes
from the station (`every:` on the station line, default 30m). A hand-written
gate for the same baton is reported as a duplicate rather than silently doubled.
This also closes the gap where an agent receives a baton it has no gate for — a
QA agent assigned an issue but woken only by the reviewer field.

## 7. Rules: enforced, not told

The `rules:` block is a guard rail, not prompt text — principle 7 of
[`README`](README.md): what must not happen is stopped outside the runtime.

Today's guard rails match on `system:action` ([`06`](06-observability-control.md)).
A rule like "assign yes, comment no — in this project" needs one more dimension:
the **project** an action touches. Two pieces:

- the target-system plugin names, per action, which parameter carries the
  project (`project_id` for GitLab) — again in the `Descriptor`;
- the guard-rail engine gains a `project` match next to `system:action`, and a
  workflow's rules compile into rules at the agent level for every agent that
  fills one of its stations: `deny gitlab:comment project=customer/tickets`.

The prompt section still states the rules — an agent that knows them does not
waste turns running into them — but the deny is what holds. A denied action is
recorded with the workflow as its source, so that "why could it not comment"
has an answer in the recording.

## 8. Position and dwell

The control plane already polls the target systems for gates. A workflow reuses
that: for every open item in its scope it reads the batons and derives **where
the item is** and **since when** — the time the baton last changed, from the
item's history where the target system has one, otherwise from the first poll
that saw it.

Two things are built on that:

- **A view** per workflow: stations as columns, items as cards, the dwell time
  on each card, the resolved agent on each column (marked when a station is
  skipped or falls to the owner). It is a reading of the target system, so it
  is never out of step with it for longer than a poll.
- **Dwell alerts**: an item lying at a station longer than its `dwell:` tells the
  owner, once, in their conversation with the platform
  ([`28`](28-team-surface.md)). Following "a check that can never be satisfied
  is furniture" (CLAUDE.md), the alert ends by itself when the item moves, and
  the owner can end it by recording that they have taken it — then it does not
  come back for that item at that station.

Items that leave the workflow — closed, merged and done at the last station,
moved out of the scope — drop out of the view.

## 9. What stays the same

- **Heartbeats, tasks and the backlog** ([`03`](03-lifecycle-scheduling.md)) are
  unchanged. A workflow wakes agents through gates and leaves the task model
  alone; `covey/create_task` between agents stays possible for work that is not
  a hand-over along a path.
- **Stages** stay an agent's own kanban columns. A workflow's stations are a
  different thing: they are shared, and they are read from the target system.
- **The depth counter** keeps working outside workflows; inside one, `rounds:`
  sets its limit per pair of stations.

## 10. Thin slice and order

Following "the thinnest vertical slice first":

1. **Stations and resolution.** The `workflows` table and file, scope and
   stations only (no `flow`, `rules`, `dwell`), the assignment table, resolution
   at dispatch with the skip-or-owner rule, and the prompt section listing the
   resolved hand-over target. One real path — build → review (optional) →
   acceptance — described this way, and the names taken out of its agents'
   playbooks. *Done when* stopping the review agent makes the next run of the
   developer hand straight to acceptance without a config change.
2. **Derived gates.** Batons → gates, the `workflow` source in the heartbeat tab,
   duplicate detection.
3. **Position and dwell.** The view and the alerts.
4. **Rules as guard rails.** The `project` match in the guard-rail engine, the
   plugin declaring the project parameter, compilation from `rules:`.
5. **`flow` and `rounds`.** Transitions in the prompt section, the round limit
   per pair of stations.

Slice 1 needs no plugin change and no guard-rail change; it is the part that
removes names from playbooks, which is where most of today's failures start.

## 11. Open questions

- **Where does the file live?** An organisation object in the database, edited
  in the interface and exported like an agent bundle — or a file in a repository
  the organisation names, synced? Config as code argues for the second; every
  other organisation object today is the first.
- **Who may change a workflow?** It decides who works on what for several agents
  at once — closer to the org chart than to one agent's config. A role of its
  own, or `org_admin` only?
- **Outcomes as events.** `on:` is documentation in the first slices. Making it
  an event (the agent reports "approved", the control plane checks the baton
  moved accordingly) would let the platform catch a hand-over that went to the
  wrong station — at the cost of a new action and a contract every agent has to
  keep.
- **Should the control plane ever move a baton itself?** When a station falls to
  the owner, the item still sits with the stopped agent in the target system
  until someone moves it. Moving it would make the view true faster; it would
  also make covey an actor in a customer's system, which slice 1 avoids on
  purpose.
- **Items without a baton.** A ticket nobody is assigned to is in no station.
  Is that "not in the workflow yet", or a station of its own (`intake`) the
  workflow should be able to name?
- **Several workflows over one project.** Two workflows whose scopes overlap
  could both claim an item. Refuse overlapping scopes at save time, or let a
  label or a path pattern decide?
