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
- kennzahl: geloeste-tickets  titel: Gelöste Tickets       zählt: aktion zammad:reply_external  je: ticket_id  ziel: 20 pro woche
- kennzahl: code-reviews      titel: Code-Reviews          zählt: aktion gitlab:comment_mr      je: mr_iid
- kennzahl: bugs-erfasst      titel: Bug-Reports erfasst   zählt: aktion gitlab:create_issue
- kennzahl: abgegeben         titel: An Menschen abgegeben zählt: aktion zammad:escalate        je: ticket_id
- kennzahl: laeufe-erledigt   titel: Erledigte Aufgaben    zählt: aufgabe erledigt
```

Deliberately few rule forms, because every additional one is a query shape that has to stay fast:

- `aktion <system>:<verb>` — one successful target-system action. Failed actions do not count; an agent that tried to close a ticket and got a 422 resolved nothing.
- `aktion <system>:*` — any action of a system, for a coarse "did it touch GitLab at all".
- `aufgabe erledigt` — a backlog task that reached `done`, optionally narrowed by `herkunft:` (origin).

### `je:` counts objects, not events

The most important qualifier, and the one that decides whether the figures mean anything. **"Tickets resolved" is a count of tickets, not of replies.** An agent that answers five times in the same ticket produced five `reply_external` events and resolved one ticket; counted raw, its unit cost drops to a fifth and it looks excellent for having been chatty.

`je: <param>` therefore turns the `COUNT(*)` into a `COUNT(DISTINCT params->>'<param>')` over the period. The parameters are in the event payload, so this needs no new instrumentation — but it does mean a rule without `je:` should be the exception, chosen deliberately (as with `bugs-erfasst` above, where every event really is a new object).

**`kennzahl:` is the comparison anchor.** Two agents that both define `geloeste-tickets` are counted into the same column org-wide, even though each defines it in its own config. That gives per-agent freedom (a support agent has no code reviews; a QA agent has no tickets) without an org-wide catalogue having to exist first. A catalogue of templates can sit on top later — it does not need to come first.

### A rule that counts nothing has to say so

If a plugin renames an action, the rule keeps parsing and silently counts zero — and zero reads like a lazy agent, not like a configuration error. The platform already has the place for this: the **config lint** above the tab bar on the agent page (`GET /agents/{id}/lint`), the same one that reports frequent turn-limit aborts. Its `Subject` takes runtime figures alongside the config (`TurnLimitFailures`); a per-indicator match count fits the same way.

The rule: an indicator that matched **nothing** while the agent did run is a `warn` finding naming the action it waits for. Deliberately not a parse error — a fresh agent has no matches yet, and a lint that nags at correct configs is a lint nobody reads.

Validating the action name against a catalogue instead would be better, but only works halfway today: manifest plugins carry their actions (`Actions map[string]ManifestAction`), the built-in ones (GitLab, Zammad, GitHub) expose only `ActionSubject` and `PromptDoc`. Giving `target.System` an `Actions() []string` is the clean fix and can come later — the empirical hint carries the case that actually hurts.

### `KPIS.md` is not compiled into the prompt

Like `ACCESS.md`, this file is **not** part of the system prompt. That is not an optimisation, it is the point: an agent that knows it is measured on the number of comments writes more comments. The measurement has to stay outside the measured system, otherwise the first thing the KPI reports is its own corruption.

## Holding it against the cost

This is what the whole thing is for. Three derived figures, in the order of how much they say:

**Unit cost, per indicator** — total cost ÷ count, computed for **each** indicator separately, never summed across them:

```
Tickets resolved      142      3.20 $ / unit
Code reviews           38      1.05 $ / unit
Bug reports filed      12      0.90 $ / unit
Handed to a human      19      —
Runs failed             7      —
```

The **count carries as much as the price** and is shown next to it, never replaced by it: 3.20 $ per ticket says nothing about whether the agent handled five tickets or five hundred, and a unit cost computed over three events is noise ranked as if it were a measurement. Below a minimum count the price is therefore left out and only the raw figure stands.

The last two lines are the counter-figures, and without them the price list rewards shirking: an agent that hands every hard case to a human has excellent unit costs on the easy remainder. **Handing over is itself an action** — Zammad, GitLab and GitHub each have an `escalate` action, so it is counted by exactly the same mechanism and needs no special path. It is only *displayed* differently: beside the delivery, without a price, because it is not a service being bought. Tasks that ended in `failed`/`cancelled` sit next to it for the same reason.

This is the figure a managing director understands, and the only one that can be held against a human hourly rate. Reading the workforce as a **price list** also sidesteps the question of which indicator "counts": nothing is added up across kinds, a support agent and a QA agent simply carry different lines.

Two properties of the number have to be stated wherever it appears, because both invite a wrong reading:

- **The column must not be totalled.** Each line divides the agent's *entire* cost by the count of that one indicator — a full-cost attribution that answers its own question ("what does a resolved ticket cost me, everything in"). For an agent with three indicators, the same dollars appear in three lines.
- **Idle runs are in the numerator, deliberately.** The alternative — attributing only the cost of the runs that produced the indicator — drops the runs that delivered nothing, although they are paid for, and forces a split for a run that resolves a ticket *and* files a bug. That variant belongs next to the figure as detail, not in its place. For the common case, an agent with one dominant indicator, the two nearly coincide.

Org-wide, indicators with the same `kennzahl:` key are summed across agents, and the denominator is the cost of exactly those agents that carry the key — otherwise the price of a resolved ticket would include a QA agent that never touched one.

**Productive share** — the share of runs in which at least one KPI event occurred. This extends the `actions = 0` signal that the run-cost list already carries: an agent whose heartbeat wakes it forty times a day and delivers on three of them is not a cheap agent, it is a badly scheduled one. This figure is what tells them apart.

**The two curves together** — cost and KPI count over the same time axis, with unit cost as the derived line. A rising cost curve is neutral information; a rising cost curve next to a flat delivery curve is a finding.

Because every KPI event hangs off a task and every task has costs, a fourth view is available cheaply — "what did the runs that produced this KPI cost" — but it stays secondary. It answers a diagnostic question, not the budget question.

## Did it pay off — the figures that qualify the price

A price says what a result cost, not whether the result was any good. Four figures qualify it, and three of them need no new instrumentation at all — the data is already recorded, it is only not evaluated.

### Rework rate — did the case come back?

The strongest quality proxy that gets by without a judgement. A ticket resolved today and reopened on Thursday was not resolved, and today it counts as delivery all the same.

The mechanism is already in place, from the other side: `CorrelateWake` only wakes tasks in state `blocked`. An event arriving on a `correlation_key` whose task has already reached `done` therefore finds nothing to wake and produces a **new** task carrying the same key — which is exactly what a returning case looks like. `task_transitions` holds the history to go with it.

The rework rate is then the share of objects that reappeared within a window after being closed. The window has to be chosen and named (seven days is a reasonable default for tickets, useless for merge requests), and it belongs next to the indicator rather than in a corner of its own: an indicator at 90 % rework is not delivery, it is a holding pattern, and its unit cost is fiction.

### Rejection rate at the approval gates

Here a human has already judged, and the verdict is recorded without interpretation in `approvals.status`. It is the only figure in this document that is not a proxy — somebody looked at a proposed action and said no. An agent whose proposals are rejected a third of the time produces review work rather than work.

The same family: guard-rail hits — how often the agent attempted something it is not allowed to do. Not a performance figure, but it belongs in the same picture.

### Response and lead time

The value that has nothing to do with money, and is often the actual one. Time from the incoming event to the agent's first action, derivable from the task's `created_at` and the first `action` event of its run. As a **median, not a mean** — one run that hung for six hours must not make the picture look bad, and one that hung for six hours must not disappear into an average either, so the spread belongs next to it.

An agent that answers at three in the morning in forty seconds delivers value even when it is not cheaper per ticket than a person. Without this figure the evaluation measures agents only on the axis where they convince least.

### A reference value — and it stays an assumption

"3.20 $ per ticket" only becomes a statement once what the alternative costs stands next to it. That is not a measurement but a parameter: a reference value per indicator, configured ("a ticket ties up twelve internal minutes"), off which a comparison can be computed.

Two rules for it, and they matter more than the feature: it is **labelled as an assumption** wherever it appears, and the platform never merges it with the measured figures into one number. A "saving of 40,000 €" that is in truth somebody's estimate multiplied by a real count is the fastest way to lose the credibility that every other figure in this document was built to earn.

### The limit: the honest answer needs a before

Even all four together remain a bundle of proxies. The one hard proof would be a before-and-after against the target system's own history — how many tickets a week ran through before the agent, how fast, with what return rate. That data sits in the target system, not here; a one-off import when a system is connected would be the way to it.

It is considerably more work than everything else in this document, and it is the difference between "the agent handles 142 tickets" and "the agent handles 142 tickets that somebody else used to handle".

## Where it shows: no new view

Performance is not a second subject next to cost, it is the other half of the same one — so it does not get a page of its own. The **cost page turns into the economics page**, and every element it already has takes the second figure alongside:

| Element there today | What it becomes |
|---|---|
| Four tiles, three of them token variants (input / output / total) | one of those slots becomes *delivered* — the count of the leading indicator for the current filter. The first row then answers both halves instead of three variants of one |
| *By model* bars (a list with a figure per row) | the same shape carries the **price list**: one row per indicator, count and unit cost. It is the block the whole feature is for, and it needs no new kind of element |
| Chart toggle `cost \| tokens` | a third position *unit cost*, the derived curve over the same time axis — one more segment, not another chart |
| *By agent* bars, sorted by USD | unit cost as the second figure on the bar: "which agent is expensive" becomes "which agent is expensive **per result**" |
| *Costliest runs* with its `actions` column and `idle` marker | the run's KPI hits replace the raw action count — same table, sharper column |

The price list carries the counter-figures (handed over, failed) in the same block, so nobody has to look for them somewhere else to read the prices correctly. The **rework rate belongs in the same row as its indicator** for the same reason — a price whose quality figure sits on another screen gets quoted alone. Rejection rate, guard-rail hits and response time are per-agent figures and sit in the *Performance* block of the employee profile, where the run history already is.

```
Gelöste Tickets       142      3,20 $ / Stück      12 % zurückgekommen
Code-Reviews           38      1,05 $ / Stück       —
An Menschen abgegeben  19      —
```

The same on the data side: the indicators travel in the existing `OrgCostReport`, not through an endpoint of their own. One request, one filter bar, one period — the two sets of numbers cannot drift apart, and the period selector does not have to be built twice.

Exactly **one** place outside it: the **employee profile**, where the cost bar already sits. That is the HR view of the individual agent — a *Performance* block next to it, not a new menu entry. The org dashboard gets no aggregation of its own; it links to the cost page.

## Implementation sketch

**Aggregate live, do not materialise counters.** A KPI defined today then shows its history immediately, and a rule that turns out to be wrong can be corrected without a data migration and without a gap in the series. A counter table would fix today's definition into the past and would have to be backfilled on every change — the exact opposite of config as code.

That puts the load on `recording_events`. The existing indexes (`agent_id, id` and `task_id, id`) do not carry an aggregation over kind, action and period; the report needs one on `(agent_id, kind, created_at)` plus access to `payload->>'action'`. **As an expression index, not a generated column** — `ADD COLUMN … GENERATED` rewrites the table and locks it while it does, which on a running instance is a maintenance window; `CREATE INDEX CONCURRENTLY … ((payload->>'action'))` gets there without one. On a table that only ever grows, that difference decides whether the migration is deployable at all.

If the scans do become expensive at some point, the answer is a **daily roll-up** as a cache in front of the live query — not instead of it. The live path stays the definition of truth.

Two invariants this rests on, worth writing down because both are quiet today and would be expensive to discover later:

- **Every cost entry hangs off a task.** `cost_entries.task_id` is nullable, but there is exactly one caller of `AddCost` (`orchestrator.go`) and it always passes one. If costs without a task ever get booked — a maintenance action, a system run — they silently leave the denominator and every unit cost drops. That is a thing to notice at the moment it is introduced, not afterwards from a suspiciously good price.
- **`recording_events` has no retention.** The whole approach depends on it. If one is ever introduced, the roll-up has to exist *first*, or the history of every indicator disappears with the events.

Who may see the figures is decided deliberately, not inherited: performance data per agent is more sensitive than a cost total, and "whoever may see costs may see this" is an answer, but it should be a chosen one.

## Limits, stated plainly

A KPI counts events. It does not judge them. A ticket closed wrongly counts exactly like one resolved well, and an agent steered on comment count will comment more. That is Goodhart's law and no schema fixes it — which is why the numbers here are **reporting**, not a control loop: nothing in the platform rewards an agent for its KPI, nothing shows it its target, and no scheduler decision hangs off one.

The qualifying figures above narrow the gap but do not close it. Rework, rejections and response time are **proxies for quality, not quality**: a case that never comes back may have been resolved well or merely abandoned by a resigned reporter, and an approval nobody rejects may mean the proposals were good or that the reviewer stopped reading. They are worth having because they are cheap and because they move in the right direction — not because they settle the question.

The judgement itself stays where it already sits: in the recording, in the approval gates, and in the humans who read both.

---

**Related:** [`06-observability-control.md`](06-observability-control.md) (recording, cost control) · [`02-agent-model.md`](02-agent-model.md) (config as code) · [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md) (backlog, heartbeat)
