# 17 — Performance indicators (what the workforce delivers)

The platform answers what an agent **costs** — per run, per day, per model, and since recently on the backlog card of the individual task ([`06-observability-control.md`](06-observability-control.md)). It does not answer what the agent **delivered**. An agent is therefore visible as a cost centre and as nothing else, and every conversation about whether it is worth it ends in an anecdote.

The HR metaphor demands the other side. A human employee is not steered by their salary alone but by what they got done: tickets resolved, reviews written, bugs triaged. The platform can say the same about its agents — the evidence is already in the database.

## The principle: a KPI is a counting rule over recorded evidence

**The agent does not report its own numbers.** This is the decisive design point and everything below follows from it.

An agent asked "how many tickets did you resolve today" answers with a plausible number. It is a language model; a self-report is a sentence, not a measurement. A number produced that way cannot carry a budget decision, and it moves the moment somebody rewrites a prompt — or an attacker writes into a ticket that the agent should report a good week.

A KPI here is therefore defined as a **counting rule over events the control plane recorded itself**: the actions the action proxy executed against a target system, and the state transitions of the backlog. Both are written outside the runtime, by the control plane, at the moment they happen. The agent cannot write them, cannot revise them, and cannot talk them up.

### No model is involved in the counting

Worth stating explicitly, because "KPIs for AI agents" invites the assumption that something judges: the path from event to figure runs entirely through Go and SQL.

The action proxy writes the record itself, after the call returned ([`internal/daemon/actionproxy.go`](../internal/daemon/actionproxy.go)):

```go
data, err := p.execute(ctx, system, action, params)
auditMap := map[string]any{"action": subject, "params": params, "ok": err == nil}
```

`subject` is the action name from the routing, not from the model's answer; `ok` is the plugin's verdict on the call. The runtime chooses *whether* to invoke an action — it does not get to name it, and it cannot claim success. The orchestrator stores the record unchanged.

The rule is then a string comparison: `zählt: aktion gitlab:comment_mr` becomes `WHERE kind='action' AND payload->>'action'='gitlab:comment_mr' AND payload->>'ok'='true'`. Same data plus same rule gives the same figure, today and in a year, and every figure can be unfolded down to the individual events behind it.

The one place a model would be needed is the question whether a closed ticket was closed *rightly*. That is a judgement, not a count, and it has no business in this number — see the limits at the end.

### The figure is current, not daily

There is no batch run. The indicator is a query, not a stored counter: every view computes it on request over the events present at that moment, and the action event is in the database in the same second the agent acts. A period like "today" is a `WHERE` on `created_at`, not a storage interval.

It can even count up without a reload. The orchestrator already publishes a live event for every action (`Event{Type: "recording", …}`) and the SPA hangs off that stream — hooking the indicator query into the same invalidation is one line. The daily roll-up mentioned under [implementation](#implementation-sketch) is therefore a cache for *past* periods only; the running day stays live. A roll-up that determined today's figure would show every agent as it was yesterday, which is the opposite of what the number is for.

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

**Unit cost, per indicator** — total cost ÷ count, computed for **each** indicator separately, never summed across them:

```
Tickets resolved      142      3.20 $ / unit
Code reviews           38      1.05 $ / unit
Bug reports filed      12      0.90 $ / unit
```

This is the figure a managing director understands, and the only one that can be held against a human hourly rate. Reading the workforce as a **price list** also sidesteps the question of which indicator "counts": nothing is added up across kinds, a support agent and a QA agent simply carry different lines.

Two properties of the number have to be stated wherever it appears, because both invite a wrong reading:

- **The column must not be totalled.** Each line divides the agent's *entire* cost by the count of that one indicator — a full-cost attribution that answers its own question ("what does a resolved ticket cost me, everything in"). For an agent with three indicators, the same dollars appear in three lines.
- **Idle runs are in the numerator, deliberately.** The alternative — attributing only the cost of the runs that produced the indicator — drops the runs that delivered nothing, although they are paid for, and forces a split for a run that resolves a ticket *and* files a bug. That variant belongs next to the figure as detail, not in its place. For the common case, an agent with one dominant indicator, the two nearly coincide.

Org-wide, indicators with the same `kennzahl:` key are summed across agents, and the denominator is the cost of exactly those agents that carry the key — otherwise the price of a resolved ticket would include a QA agent that never touched one.

**Productive share** — the share of runs in which at least one KPI event occurred. This extends the `actions = 0` signal that the run-cost list already carries: an agent whose heartbeat wakes it forty times a day and delivers on three of them is not a cheap agent, it is a badly scheduled one. This figure is what tells them apart.

**The two curves together** — cost and KPI count over the same time axis, with unit cost as the derived line. A rising cost curve is neutral information; a rising cost curve next to a flat delivery curve is a finding.

Because every KPI event hangs off a task and every task has costs, a fourth view is available cheaply — "what did the runs that produced this KPI cost" — but it stays secondary. It answers a diagnostic question, not the budget question.

## Where it shows: no new view

Performance is not a second subject next to cost, it is the other half of the same one — so it does not get a page of its own. The **cost page turns into the economics page**, and every element it already has takes the second figure alongside:

| Element there today | What it becomes |
|---|---|
| Four tiles, three of them token variants (input / output / total) | one of those slots becomes *delivered* — the count of the leading indicator for the current filter. The first row then answers both halves instead of three variants of one |
| *By model* bars (a list with a figure per row) | the same shape carries the **price list**: one row per indicator, count and unit cost. It is the block the whole feature is for, and it needs no new kind of element |
| Chart toggle `cost \| tokens` | a third position *unit cost*, the derived curve over the same time axis — one more segment, not another chart |
| *By agent* bars, sorted by USD | unit cost as the second figure on the bar: "which agent is expensive" becomes "which agent is expensive **per result**" |
| *Costliest runs* with its `actions` column and `idle` marker | the run's KPI hits replace the raw action count — same table, sharper column |

The same on the data side: the indicators travel in the existing `OrgCostReport`, not through an endpoint of their own. One request, one filter bar, one period — the two sets of numbers cannot drift apart, and the period selector does not have to be built twice.

Exactly **one** place outside it: the **employee profile**, where the cost bar already sits. That is the HR view of the individual agent — a *Performance* block next to it, not a new menu entry. The org dashboard gets no aggregation of its own; it links to the cost page.

## Implementation sketch

**Aggregate live, do not materialise counters.** A KPI defined today then shows its history immediately, and a rule that turns out to be wrong can be corrected without a data migration and without a gap in the series. A counter table would fix today's definition into the past and would have to be backfilled on every change — the exact opposite of config as code.

That puts the load on `recording_events`. The existing indexes (`agent_id, id` and `task_id, id`) do not carry an aggregation over kind, action and period; the KPI report needs one on `(agent_id, kind, created_at)` plus access to `payload->>'action'` — practical as a generated column with its own index, so the action name does not have to be extracted from JSONB per row.

If the scans do become expensive at some point, the answer is a **daily roll-up** as a cache in front of the live query — not instead of it. The live path stays the definition of truth.

## Limits, stated plainly

A KPI counts events. It does not judge them. A ticket closed wrongly counts exactly like one resolved well, and an agent steered on comment count will comment more. That is Goodhart's law and no schema fixes it — which is why the numbers here are **reporting**, not a control loop: nothing in the platform rewards an agent for its KPI, nothing shows it its target, and no scheduler decision hangs off one.

Quality stays where it already sits: in the recording, in the approval gates, and in the humans who read both.

---

**Related:** [`06-observability-control.md`](06-observability-control.md) (recording, cost control) · [`02-agent-model.md`](02-agent-model.md) (config as code) · [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md) (backlog, heartbeat)
