# 17 — Performance indicators (what the workforce delivers)

The platform answers what an agent **costs** — per run, per day, per model, and since recently on the backlog card of the individual task ([`06-observability-control.md`](06-observability-control.md)). It does not answer what the agent **delivered**. An agent is therefore visible as a cost centre and as nothing else, and every conversation about whether it is worth it ends in an anecdote.

The HR metaphor demands the other side. A human employee is not steered by their salary alone but by what they got done: tickets resolved, reviews written, bugs triaged. The platform can say the same about its agents — the evidence is already in the database.

## The principle: a KPI is a counting rule over recorded evidence

**The agent does not report its own numbers.** This is the decisive design point and everything below follows from it.

An agent asked "how many tickets did you resolve today" answers with a plausible number. It is a language model; a self-report is a sentence, not a measurement. A number produced that way cannot carry a budget decision, and it moves the moment somebody rewrites a prompt — or an attacker writes into a ticket that the agent should report a good week.

A KPI here is therefore defined as a **counting rule over events the control plane recorded itself**: the actions the action proxy executed against a target system, and the state transitions of the backlog. Both are written outside the runtime, by the control plane, at the moment they happen. The agent cannot write them, cannot revise them, and cannot talk them up.

## What is countable today

No new instrumentation is needed for the first version. Three tables already carry it:

| Source | What it holds | Usable as |
|---|---|---|
| `recording_events`, `kind='action'` | `{"action":"gitlab:merge_mr","params":…,"ok":true}` — per task, per agent, timestamped | the work itself: replies sent, MRs commented, issues created, pages written |
| `backlog_tasks` | `state` (`done`/`failed`/`cancelled`), `origin`, timestamps | throughput: tasks completed, and where they came from |
| `cost_entries` | `usd` per task and agent | the other side of the ratio |

Two properties make this a sound basis rather than a convenience: the action events are written **per task**, so every countable event has a cost attached to it; and `recording_events` has **no retention** (unlike the request log), so a KPI defined today can be counted backwards over the entire history.

Examples that fall out without any new plumbing: *tickets resolved* (`zammad:reply_external`, `zammad:close_ticket`), *code reviews* (`gitlab:comment_mr`, `gitlab:approve_mr`), *bug reports filed* (`gitlab:create_issue`), *merges* (`gitlab:merge_mr`), *mails answered* (`email:reply`), *wiki pages written* (`covey:remember`).

## The definition

A KPI has a stable key, a title, a counting rule and — optionally — a target. It is **config as code**, like everything else about an agent's behaviour: versioned, changed by review, not by a click that leaves no trace.

`KPIS.md`, in the line style of `HEARTBEAT.md` (German keys, because a parser reads them and the other config files do it the same way):

```
- kennzahl: geloeste-tickets  titel: Gelöste Tickets       zählt: aktion zammad:reply_external  ziel: 20 pro woche
- kennzahl: code-reviews      titel: Code-Reviews          zählt: aktion gitlab:comment_mr
- kennzahl: bugs-erfasst      titel: Bug-Reports erfasst   zählt: aktion gitlab:create_issue
- kennzahl: laeufe-erledigt   titel: Erledigte Aufgaben    zählt: aufgabe erledigt
```

Deliberately few rule forms, because every additional one is a query shape that has to stay fast:

- `aktion <system>:<verb>` — one successful target-system action. Failed actions do not count; an agent that tried to close a ticket and got a 422 resolved nothing.
- `aktion <system>:*` — any action of a system, for a coarse "did it touch GitLab at all".
- `aufgabe erledigt` — a backlog task that reached `done`, optionally narrowed by `herkunft:` (origin).

**`kennzahl:` is the comparison anchor.** Two agents that both define `geloeste-tickets` are counted into the same column org-wide, even though each defines it in its own config. That gives per-agent freedom (a support agent has no code reviews; a QA agent has no tickets) without an org-wide catalogue having to exist first. A catalogue of templates can sit on top later — it does not need to come first.

### `KPIS.md` is not compiled into the prompt

Like `ACCESS.md`, this file is **not** part of the system prompt. That is not an optimisation, it is the point: an agent that knows it is measured on the number of comments writes more comments. The measurement has to stay outside the measured system, otherwise the first thing the KPI reports is its own corruption.

## Holding it against the cost

This is what the whole thing is for. Three derived figures, in the order of how much they say:

**Unit cost** — total cost ÷ KPI count. "3.20 $ per resolved ticket." It is the number a managing director understands, and the only one that can be held against a human hourly rate. Deliberately computed over **all** costs of the period, including the runs that delivered nothing: the idle runs are paid for too, and a unit cost that quietly drops them flatters the agent.

**Productive share** — the share of runs in which at least one KPI event occurred. This extends the `actions = 0` signal that the run-cost list already carries: an agent whose heartbeat wakes it forty times a day and delivers on three of them is not a cheap agent, it is a badly scheduled one. This figure is what tells them apart.

**The two curves together** — cost and KPI count over the same time axis, with unit cost as the derived line. A rising cost curve is neutral information; a rising cost curve next to a flat delivery curve is a finding.

Because every KPI event hangs off a task and every task has costs, a fourth view is available cheaply — "what did the runs that produced this KPI cost" — but it stays secondary. It answers a diagnostic question, not the budget question.

## Where it shows

- **Employee profile** — a *Performance* section next to the cost bar. This is where the HR metaphor pays off: one page that says what this employee costs and what they deliver.
- **Cost page** — a unit-cost column per agent, so the ranking of expensive agents can be read against their output instead of alone.
- **Org dashboard** — the sum over the workforce, and the outliers in both directions.

## Implementation sketch

**Aggregate live, do not materialise counters.** A KPI defined today then shows its history immediately, and a rule that turns out to be wrong can be corrected without a data migration and without a gap in the series. A counter table would fix today's definition into the past and would have to be backfilled on every change — the exact opposite of config as code.

That puts the load on `recording_events`. The existing indexes (`agent_id, id` and `task_id, id`) do not carry an aggregation over kind, action and period; the KPI report needs one on `(agent_id, kind, created_at)` plus access to `payload->>'action'` — practical as a generated column with its own index, so the action name does not have to be extracted from JSONB per row.

If the scans do become expensive at some point, the answer is a **daily roll-up** as a cache in front of the live query — not instead of it. The live path stays the definition of truth.

## Limits, stated plainly

A KPI counts events. It does not judge them. A ticket closed wrongly counts exactly like one resolved well, and an agent steered on comment count will comment more. That is Goodhart's law and no schema fixes it — which is why the numbers here are **reporting**, not a control loop: nothing in the platform rewards an agent for its KPI, nothing shows it its target, and no scheduler decision hangs off one.

Quality stays where it already sits: in the recording, in the approval gates, and in the humans who read both.

---

**Related:** [`06-observability-control.md`](06-observability-control.md) (recording, cost control) · [`02-agent-model.md`](02-agent-model.md) (config as code) · [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md) (backlog, heartbeat)
