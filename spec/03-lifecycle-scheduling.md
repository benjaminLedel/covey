# 03 — Lifecycle & scheduling

This is the heart of the platform. The scheduler/dispatcher is the actual product: an OS scheduler + cron + inbox + state management for agents.

## The "always-on" trick

An agent should behave like an employee: always reachable, with a backlog, proactive. But **"always-on" is a UX property, not a runtime property.** A human is permanently there yet burns almost no energy doing it — they become active when something comes in. Literally always-on (continuous inference) would be expensive and would produce hallucination noise.

The solution: **always reachable and stateful, but compute only on demand.** The vehicle for that is the backlog plus a cheap dispatch loop.

## Dispatch loop

A permanent, **cheap** dispatch loop runs per agent — **no LLM**, pure orchestration. It knows three wake sources:

| Wake source | Example |
|---|---|
| **Event** | new ticket, webhook, incoming mail (if the agent has one), delegation from another agent |
| **Scheduler tick** | every N minutes, "anything to do?" |
| **Schedule (cron)** | "Monday 9 a.m. weekly report" — configured per agent in `HEARTBEAT.md`, see below |

Only when one of these sources fires is the expensive agent runtime in the sandbox woken (`wake` → `assign_task`, see [`01-architecture.md`](01-architecture.md)).

An agent that has not been **hired** yet is skipped by the loop entirely, whichever source fires: a draft has no first day, so it has no waking phases ([`02-agent-model.md`](02-agent-model.md)). Its heartbeat is not scheduled and its webhook is not live; tasks queued against it simply wait, and the hiring is what releases them.

### Heartbeat: recurring tasks (`HEARTBEAT.md`)

The schedule source is config as code: every line in `HEARTBEAT.md` ([`02-agent-model.md`](02-agent-model.md)) defines a recurring task.

```markdown
- alle: 30m      titel: Posteingang sichten   aufgabe: Prüfe neue Tickets und triagiere sie.
- täglich: 09:00 titel: Tagesbericht          aufgabe: Fasse den gestrigen Tag zusammen.
- alle: 5m nur-wenn: email titel: Postfach    aufgabe: Bearbeite die ungelesenen Mails.
```

Two schedule forms, exactly one per entry:

- **`alle:`** (every) — an interval (`30m`, `2h`, `1d`). Due as soon as the interval has elapsed since the last run.
- **`täglich:`** (daily) — a fixed time of day (`HH:MM`, server time). Due once per day from that time onward.

Optional per entry:

- **`nur-wenn:`** (only-if) — the name of a target system that has to report work first when the entry fires. The control plane asks the plugin through the optional `target.WorkChecker` interface (`HasWork`) with self-resolved secrets — for `email`, for instance: "are there unread mails in the working set?" If the system reports no work, the run is skipped without waking the agent; `last_fired_at` is advanced regardless, so the schedule keeps polling as usual. The check is fail-open: if the condition cannot be evaluated (plugin without `WorkChecker`, missing secrets, connection error), the heartbeat fires as usual — a broken condition must not leave work lying around. This turns the polling intake of webhook-less systems into a cheap control-plane check; the expensive runtime only starts when something is genuinely there.

**Never wake twice on the same state (signature):** a `nur-wenn:` condition measures a level — "something is waiting there" stays true until the agent changes the state. For conversation threads (GitLab issue, merge request) that means: as long as the last contribution is not from the agent, the same item wakes it again at every interval. An agent that **deliberately** ends a run without commenting — the QA colleague's feedback was an approval, there is nothing to do — would therefore be woken permanently and would end up commenting only to switch off its own alarm clock. Between two agents this carries a runaway: every comment closes one's own gate and opens the other's.

Plugins can therefore deliver, via `target.SignedWorkChecker`, a **signature** of the work found alongside the yes/no — a short, stable description of what the check responded to (GitLab: project, item number and highest note ID per waiting thread; pushes count, because GitLab records them as a system note). The control plane keeps it in `agent_heartbeats.last_work_sig` and only fires when it has **changed**. So the agent is still woken for every piece of news — including one it merely takes note of — but never twice for the same one. The decision whether feedback means work (defects reported) or not (approval) thereby sits with the agent instead of the gate; **silence becomes a valid answer**. If a plugin delivers no signature, the heartbeat fires on every level as before (fail-open). When the check reports no more work, the signature is reset — the same state may wake the agent again later.

**The watermark after a run.** The signature is remembered at *dispatch*, i.e. before the run has done anything — a run that comments changes it itself, so the control plane advances it once more when the run completes; without that the agent's own comment wakes it again in the next interval. The state after the run, however, also holds whatever arrived from outside meanwhile, and where several agents share one target-system identity (no bot account per role) authorship cannot tell the two apart. Whatever ends up in the watermark counts as handled, so a foreign contribution absorbed into it wakes nobody again.

The advance is therefore tied to the run having written at all: plugins report via `target.SignatureWriter` which of their actions can move the signature (GitLab: everything that produces a note, including the system notes for assign, label, approval, push and merge), and the control plane compares that against the actions recorded for the run — the whole chain, a continuation included. A run that only read leaves the watermark alone, so the next tick fires for what arrived. What that does not cover is a foreign contribution to exactly the thread the agent commented on itself, within exactly that window; separating that would require the ids of the notes the agent wrote, and the recording holds the action, not the note it produced.

A run that ends **without** a result (turn limit, escalation, error) does the opposite and releases the signature: the work it was woken for is still lying there.

Mechanics: on saving the config, the entries are materialised into `agent_heartbeats` (`titel:` is the key; `last_fired_at` survives config versions and starts at *now* — a freshly created heartbeat therefore fires only after its schedule has elapsed, not immediately). The dispatch loop's periodic tick checks due entries via SQL and creates them as a regular backlog task with `origin='heartbeat'` — from there the normal lifecycle applies (wake, triage, working). The kill switch and the fleet-wide emergency stop suppress firing.

**No pile-up:** if the last run's task is not yet terminal (open/in_progress/blocked), no duplicate is created; the run still counts as fired, so that after completion the regular schedule continues instead of firing immediately. Missed runs (control plane down) are not caught up — at most the next due run fires.

**Manual trigger:** `POST /api/v1/agents/{id}/heartbeats/{name}/fire` (roles: org_admin, agent_owner — the "Run now" button in the heartbeat tab) fires a heartbeat immediately, regardless of the schedule. The same rules as for the tick apply: kill switch/emergency stop and a still-open task from the last run cause a refusal (409), and `last_fired_at` is advanced — the regular schedule continues counting from the manual run. A `nur-wenn:` condition is **not** checked on a manual trigger: whoever presses the button wants the run.

**Tiered cost at the tick:** the tick must not fire up Opus every time. A small, cheap model first decides "is there anything to do at all?" — on "no" the agent stays asleep. Only on "yes" is the full runtime woken. The tick is what produces proactivity: without an external trigger, the support agent notices for itself that "ticket #42 has been waiting two days for a customer reply, I'll follow up."

### Generic webhook trigger (optional per agent)

Besides the target-system webhooks (plugin, signature check, correlation — see [`13-zammad-integration.md`](13-zammad-integration.md)), every agent can **optionally** be given a generic webhook as a wake source — for third-party systems without a plugin of their own (CI pipelines, cron jobs, Zapier, monitoring):

- **Activation** via the API/UI (`POST /api/v1/agents/{id}/webhook`, manager roles) generates a secret token; activating again rotates it (the old URL becomes invalid), `DELETE` deactivates. The default is **off** — the token sits as a nullable column on the agent (`webhook_token`), not in the sandbox.
- **Triggering:** `POST /api/trigger/{token}` — the token in the URL is the entire authentication. Payload optionally as JSON `{"title", "body", "priority", "dedup_key"}`; any other body is taken into the task body as raw text (nothing is lost, not even foreign payload formats).
- **Effect:** creates a regular backlog task with `origin='webhook:trigger'` and kicks off dispatch immediately — from there the normal lifecycle applies. No plugin, no correlation: whoever needs correlated wakes takes a target-system plugin.
- **Idempotency:** optional via `dedup_key` (scoped per agent, the same `webhook_events` table as the event router) — the third-party system's retries do not create duplicates.
- **Fail-closed:** a stopped agent (kill switch) refuses the trigger; an unknown token returns 404.

## State machine

```
   ┌──────────┐  event/tick/schedule   ┌───────────┐
   │ sleeping │ ─────────────────────▶ │ triggered │
   └──────────┘                        └─────┬─────┘
        ▲                                    │
        │                                    ▼
        │                              ┌───────────┐
        │                              │  triage   │  check backlog + memory
        │                              └─────┬─────┘
        │                                    │
        │                                    ▼
        │                              ┌───────────┐
        │  securing                    │  working  │
        └──────────────┐               └──┬─────▲──┘
                       │                  │     │
                  ┌────┴─────┐   block    │     │  correlated event
                  │   done   │◀───────┐   ▼     │
                  └──────────┘        ┌─────────┴─┐
                                      │  blocked  │  waits for an external event
                                      └───────────┘
```

| State | Meaning | Compute |
|---|---|---|
| `sleeping` | reachable, waiting for a wake | none (dispatch loop only) |
| `triggered` | a wake source has fired | minimal (tick decision) |
| `triage` | check backlog + memory, prioritise | runtime on |
| `working` | the task is being worked on in the sandbox | runtime on (full) |
| `blocked` | task parked, waiting for an external event | none (suspended) |
| `done` | task finished, result + memory update | runtime shuts down |
| `securing` | sandbox stopped, home written into the store | runtime off, control plane busy |

The full cycle: `sleeping → triggered → triage → working → (blocked ⇄ working) → done → securing → sleeping`.

**`securing` names the phase between "the last task is done" and "the sandbox is gone"** (migration `0079_status_securing`). The run is over, the platform is not: the container is being stopped and the home written into the store — a second for a small home, half a minute of scanning for a grown one. It gets a name of its own because `working` answered *is this agent busy* with a yes that meant something else entirely, and hid the one moment an operator would want explained. During `securing` the agent is not available for work, which is true, and is what a status should say.

## The `blocked` state

The state nearly everyone forgets — and the one that turns an agent into an employee. A real employee parks tasks ("waiting for the customer's reply", "waiting for the boss's approval") instead of polling for them or hallucinating an answer.

The agent must be able to say: **"I am blocked on X, wake me when the answer arrives"** — and then *actually suspend*. The daemon reports `blocked` with a **correlation key** to the control plane; the sandbox is shut down. The `blocked → working` edge is closed when an incoming event is mapped onto that key.

Clean `blocked` handling is the difference between "agent" and "employee".

## The aborted run: turn limit instead of a result

`blocked` is the planned halt. Next to it stands the **unplanned** one: a run hits its turn limit (`max_turns`) before arriving at a result. It did work — cloned, read, half-fixed — but none of that is in a result, and the context dies with the sandbox.

Handled naively this is a silent failure, and out of it grows the most expensive loop in the system: the heartbeat fires again, a fresh run starts the same work from scratch, hits the limit again — arbitrarily often.

The runtime adapter therefore reports this case as its own status **`incomplete`** (not `failed`) and attaches two things:

1. **The handover state.** A single additional turn on the already-aborted session (`--resume`, without tools) asks the agent for *Done / Open / Next step*. On cached context that costs almost nothing compared to the run that would otherwise be lost.
2. **The runtime session** for resuming.

The control plane turns that into:

- a **note on the task** with the handover state (visible on the board, see [Notes](#notes-interim-state-on-the-task)),
- the completion of the task as `failed` — with a meaningful error text instead of an empty field — and the handover state as the result,
- a **follow-up task** (`parent_task_id`, origin `continuation:<task-id>`) that picks the same session back up and continues where the run broke off.

The follow-up task deliberately carries **the same title** as the task it came from: heartbeat dedup recognises from this that the work is still running and does not fire alongside it.

**Loop protection.** A continuation that runs into the limit again produces the next one — but not endlessly. After three continuations in a row (`maxContinuations`) the task escalates to the manager instead of continuing. Whoever has no result after four full runs does not need a fifth but a human: either the assignment is cut too large or `max_turns` is too small. Without that limit the continuation would merely replace one infinite loop with another.

The better route for the agent remains not running into the limit at all: if a task grows too large, it breaks it up itself (`covey/create_task`, see [Subtasks and delegation](#subtasks-and-delegation-coveycreate_task)) and closes the current assignment with a partial result.

## Backlog as a first-class object

The backlog is **not a transient queue** but a persistent, inspectable object in the control plane. Every task carries:

- a **state** (`open`, `in_progress`, `blocked`, `done`, `failed`, `cancelled`),
- a **priority**,
- an **origin** (who/what assigned it — `manual:<email>`, `heartbeat`, `webhook:<system>`, `webhook:trigger`, `agent:<slug>` for ones the agent created itself, `continuation:<task-id>` for the continuation of an aborted run),
- a **history** (state transitions, timestamps),
- where applicable a **correlation key** (when `blocked`),
- where applicable an **originating task** (`parent_task_id` — subtask, delegation or continuation; it also carries the loop protection, see below),
- where applicable a **stage** (a freely definable kanban column, see below),
- where applicable **notes** (the agent's proactive interim states, see below).

**Terminal states are not dead ends, and the backlog does not grow without bound:**

- **Retry:** `failed → open` and `cancelled → open` are permitted transitions — a failed or discarded task can be **rescheduled** manually (result/error are cleared, the history stays in the transitions, the agent is woken). `done` stays final.
- **Archive instead of delete:** terminal tasks (`done`/`failed`/`cancelled`) can be **archived** individually or in bulk ("Clean up") (`archived_at`). Archived means: hidden from the active backlog but fully preserved — history and recording references stay valid, and the UI shows the archive on request. Active tasks (`open`/`in_progress`/`blocked`) are deliberately not archivable.
- **Search instead of scrolling:** the board shows the tasks by column, and per column only the newest handful — deeper columns are unfolded on demand ("Show N older"). What lies further back is reached by the **backlog search** (`GET /agents/{id}/backlog?q=`): it matches title, description and what a run left behind (result/error), and it searches **archive and active board together**. That is the whole point — one searches for precisely what the board no longer shows; a search stopping at the board's edge would only find what is already in front of one. Hits come as a flat list (the column is written onto the card), newest first, capped at 50 — a search is a look, not an export.

**A lost sandbox is not a failed task — but not an infinite retry either.**

When the daemon connection drops mid-run (container killed, the host under resource pressure, a network blip), that is an infrastructure event and says nothing about the work. The task therefore goes back to `open` instead of to `failed`, and the next dispatch picks it up again. Without this, a resource spike that disconnects several sandboxes at once strands every task that was running at the time: terminal, with no automatic way back, until somebody notices and retries them by hand.

Requeueing without a limit would be right for a sporadic drop and wrong for a reproducible one — a broken sandbox image after a deploy, an OOM on container start, an agent config that reliably tears its container down. There the task circles `open → in_progress → connection lost → open`, paying for a full sandbox start every round, and nothing shows that it is stuck rather than working. After **five losses in a row** the task is therefore failed after all, with an error text naming the connection as the cause so it stays distinguishable from an error the agent itself produced. A manual retry starts from a clean count.

The count sits on the task, not in the control plane's memory: a restart is one of the things that produces these losses, so an in-process counter would be back at zero immediately afterwards. Any run that ends for a different reason — blocked, the budget stop, a manual retry — clears it. Only a genuine series counts.

### Stages: a kanban overlay on top of the state

The **state** is the machine truth — the scheduler hangs off it (`ClaimNext` takes `open`), as do `blocked` suspension and completion. It is fixed and must not become free-form, or the orchestrator loses its footing.

On top of it sits a second, **purely presentational** dimension: the **stage**. Stages are freely nameable kanban columns defined **per agent** (e.g. `Triage → Research → Waiting → Reply → Done`). They carry no semantics for the scheduler — they make visible *where in its own workflow* a task stands.

- **The agent moves itself.** Through the action proxy (`covey/set_stage`, see [`01-architecture.md`](01-architecture.md)) the agent pushes its running task into a stage; if the stage does not exist it is created automatically as a new column — so the agent "invents" its columns while working.
- **Columns are states, not headlines.** Automatic creation is convenient and tempts sprawl: an agent that coins a new name for the same activity in every run (`Issue triage`, `GitLab review`) or names the item instead of the state (`#83 CSV import`) builds itself a board with a dozen dead columns within days. The compiled system prompt therefore lays down three rules: name the *state*, reuse existing columns, make do with a handful. Whatever wants documenting beyond that is a note, not a column.
- **Auto-cleanup of agent columns.** Columns the agent created itself (`created_by='agent'`) are cleared away automatically as soon as no active (unarchived) task is left in them — checked after every stage move, after every terminal state transition and after archiving. That keeps the board free of orphaned working states. Human-created columns (UI, default stages) stay, even when empty.
- **Terminal tasks leave the agent column.** When a task reaches a terminal state, auto-follow moves it out of a *self-invented* column into "Done" as well — a finished task does not belong in "Research". Only then does the cleanup take hold: if a terminal task stayed behind in every invented column, none of them would ever be "empty", and the board would collect a dozen dead working states over weeks. Columns created by **humans** are exempt here too — deliberate placement is never overwritten.
- **The board cleans itself up.** Auto-cleanup only takes hold on *empty* columns — a finished card left lying around keeps its column alive. The control plane therefore archives, on a schedule (hourly), every terminal task untouched for longer than `COVEY_BOARD_RETENTION` (default 24 h); the agent columns thereby emptied fall away with it. Archiving is not deletion: state, history and recording remain, the task merely moves out of the active board into the archive. Deliberately time-delayed — freshly completed work should stay visible, otherwise the last run's work disappears in front of whoever wants to check it. A negative duration switches the cleanup off; the "Clean up" button on the board remains for "and do it now". Cleaning up is **hygiene, not a decision** — it belongs in the platform, not in the prompt of an agent that forgets it under load.
- **Humans likewise.** Administrators drag tasks around the board and maintain the columns (create, rename, reorder, colour, delete).
- **Persistence:** table `agent_stages` (per agent, with `position`/`color`), the task references `stage_id` (nullable → "no stage"). Deleting a stage resets affected tasks to `NULL`, never data loss.
- **Overlay, not replacement:** a task simultaneously has a `state` (e.g. `blocked`) and a `stage` (e.g. `Waiting`). The UI's kanban columns come from the stages; the state sits as a badge on the card.
- **Auto-follow of the default columns:** every agent starts with `Backlog → In progress → Done`. As long as a task sits in one of those default columns (or in none), the store advances the column automatically on a state transition (`open`→Backlog, `in_progress`/`blocked`→In progress, terminal→Done). As soon as agent or human deliberately places the task in a **custom** column, manual placement applies — auto-follow no longer touches it. If a default column is missing (renamed/deleted), the advancing simply does not happen.

### Notes: interim state on the task

Besides stage and state, a task carries **notes** (`task_notes`): proactive interim states the agent attaches mid-run through the action proxy (`covey/add_note`, see [`01-architecture.md`](01-architecture.md)) — findings, things tried, work in progress. The distinction is deliberately simple: **if it helps only this task, it is a note on the task; if it also helps future tasks, it belongs in memory** (`covey/remember`, see [`05-memory.md`](05-memory.md)). Notes hang off the task (cascade on delete, visible on the card on the board) and do not flow into the memory query of future tasks.

### Subtasks and delegation (`covey/create_task`)

Tasks do not only arise from outside (human, heartbeat, webhook, trigger). An agent can create them itself — through the meta action `covey/create_task` at the action proxy (see [`01-architecture.md`](01-architecture.md)):

- **Subtask** (without `agent`) — the agent breaks up work that is too large for one run. That is the healthy alternative to getting stuck: close with a partial result, leave the rest as a task, instead of running into the turn limit.
- **Delegation** (`"agent": "<slug>"`) — the task lands with a colleague from the same organisation and wakes them. This makes the org chart from [`02-agent-model.md`](02-agent-model.md) operational: escalation and delegation need no special protocol and no detour through an external ticket system.

Every task created this way hangs via `parent_task_id` off the task it came from and carries the origin `agent:<slug>` — so the audit records who created it and out of what.

**An agent that can create tasks can keep itself busy until the budget is empty.** Hence fail-closed in four directions:

| Limit | Rule |
|---|---|
| Policy | Guard-rail subject `covey:create_task`, on delegation `covey:create_task:foreign` — separately governable, `denied`/`pending` as for any target-system action |
| Depth | A chain of self-created tasks ends after `maxAgentTaskDepth` — no infinite decomposition |
| Width | A single run splits off at most `maxAgentTasksPerRun` tasks |
| Duplicates | If an open task with the same title already exists at the target agent, no second one is created |

Duplicate protection is the most important of the four: without it, a recurring run that creates the same task every time builds a queue that never empties — the same class of error as a heartbeat that triggers on the level instead of the edge.

Delegation stays inside the organisation: the target agent is resolved by its slug **within the sender's org**, and a paused agent accepts nothing. Inter-agent tasks are therefore subject to the same recording and policy rules as everything else (cf. the "agent-to-agent abuse" risk in [`04-identity-secrets.md`](04-identity-secrets.md)).

### Irony/opportunity: the backlog = a ticket system for agents

The backlog basically *is* a ticket system — for the agents themselves. Two options:

1. **Repurpose an existing ticket system.** Humans and agents share the same task reality; a colleague sees what the agent has on its desk and can reprioritise. Strengthens the org-chart feeling enormously.
2. **A leaner store of our own.** Less coupling, full control over the schema.

Option 1 is surprisingly powerful for the employee metaphor. Decision open — see [`07-open-decisions.md`](07-open-decisions.md).

## Serial vs. parallel

**Strictly serial within an agent:** one task at a time, the rest waits in the backlog. An LLM with one context cannot honestly juggle. Serial matches "one PC, one worker", is debuggable and consistent. Concurrency *inside* an agent is bought with massive complexity in memory and consistency.

**Parallelism = spawn more agents**, not more threads per agent. That is a cost question, not a feature question.

## Event correlation (open core question)

When agents wait blocked and are woken by events, the platform needs **reliable correlation**: the incoming event has to be mapped onto the parked task. That holds **independently of the channel** — the answer can arrive as mail, as a ticket update, as a webhook or as a message from another agent. Email is only *one* of those channels. Two approaches:

| Approach | Mechanics | Assessment |
|---|---|---|
| **Correlation key** | The agent deposits a key when going `blocked`; the outgoing prompt carries it along (task ID in the subject tag / message ID for mail, ticket ID for tickets, callback token for webhooks); the incoming event carries it back | simple, decentralised; fragile if the far end loses the key |
| **Central event router** | The control plane receives all incoming events and maps them onto agents + tasks via rules/heuristics (sender, subject, ticket ID) | more robust, centrally auditable; more logic in the control plane |
| **Hybrid** | Correlation key as the primary match, router as a fallback heuristic | pragmatic |

This decision determines how reliably parked tasks wake up again across all channels. **This is the next point to nail down** (see [`07-open-decisions.md`](07-open-decisions.md)).

## Cost consequence

Always-on × many agents only becomes affordable because **idle really is idle**. Per-agent budgets and hibernating sandboxes are not a later optimisation but a prerequisite — otherwise the bill scales away from you at the tenth agent. Details on budget tracking in [`06-observability-control.md`](06-observability-control.md).
