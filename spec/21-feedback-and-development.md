# 21 — Feedback and development (the other half of the People department)

The People department hires ([`20-hiring-and-setup.md`](20-hiring-and-setup.md)). Nobody develops. An agent is drafted, reviewed once by a human, hired — and from then on its configuration is touched only when something has already gone wrong loudly enough for a person to notice.

That is the shorter half of the job. In a company the department that recruits is the same one that holds the review, and the review is not a ceremony: it is the moment somebody who was not in the work reads what actually happened and says what should change. The platform has the material for it and no reader.

Two facts sit unused next to each other today:

- **The evidence is complete.** Every action, every state transition, every abort, every cost entry is written by the control plane, outside the runtime, at the moment it happens — and `recording_events` has no retention ([`17-kpis.md`](17-kpis.md)). What an agent did in its first month is still there in its sixth.
- **Nothing reads it as a whole.** The config lint reads a thin mechanical slice of it ([`internal/agents/lint.go`](../internal/agents/lint.go)) — heartbeats too tight, runs piling up at the turn limit, work that leaves no trace. Those rules were right about a QA agent from day one: 22 of 23 failures at the turn limit, several hundred dollars spent, not one merge request tested through. Nobody ran the check, because nobody runs a subcommand on a hunch.

This document adds the reader: a **second role in the People department** whose work is the feedback conversation. It reads what a colleague did, judges it, and proposes what to change — in the colleague's config, and where the fault is not the colleague's, in the platform's own repository.

## The one rule everything else follows from: it judges, it does not decide

[`17-kpis.md`](17-kpis.md) refuses the control loop on purpose, and the refusal is load-bearing:

> the numbers here are **reporting**, not a control loop: nothing in the platform rewards an agent for its KPI, nothing shows it its target, and no scheduler decision hangs off one. […] The judgement itself stays where it already sits: in the recording, in the approval gates, and in the humans who read both.

An agent that reads indicators and rewrites configurations closes exactly that loop, and Goodhart's law arrives with it. The resolution is not to weaken the sentence but to keep it true: **the feedback agent's output is never in effect.** It writes a proposal; a human accepts it. Nothing in the platform changes on an indicator alone, no scheduler decision hangs off one, and the last line of `17` still holds — the judgement stays with the humans, who now get it prepared instead of having to assemble it.

Three consequences, and the third is the one that is easy to miss:

1. A proposal is a **stored, inactive configuration version** — see below. There is no path from the feedback agent to a running config.
2. It **does not review itself.** The same reason `20` gives for the People department not writing its own `SOUL.md`: an agent that grades its own work at night is the door this platform keeps shut.
3. **It may not quote an agent its own figures.** `KPIS.md` is deliberately not compiled into the system prompt — *"an agent that knows it is measured on the number of comments writes more comments"* ([`internal/agents/kpi.go`](../internal/agents/kpi.go)). A proposed `SOUL.md` containing "you resolved 12 tickets last week, aim for 20" would smuggle the target into the prompt through the back door and undo that decision without anyone noticing. Feedback describes **behaviour and procedure** — "close the partial result before the turn limit, file the rest as a subtask" — never scores. Enforced by review, not by a parser: a human reads the diff, and this rule is what they are reading for.

## The personnel file: facts, not conversations

The feedback agent needs to see the work. The obvious implementation — hand it the recordings — is the wrong default in three ways at once: recordings carry ticket and mail content from other departments, so one agent would become an exfiltration path across the whole org chart; text from a target system reaching the agent that proposes configurations is the injection path from [`04-identity-secrets.md`](04-identity-secrets.md) pointed at the most valuable target on the platform; and a month of recordings does not fit in a context window at any sensible price.

So the default is a **personnel file the control plane assembles**: facts it recorded itself, per agent and period.

| Section | Source | What it says |
|---|---|---|
| Throughput | `backlog_tasks` | tasks by state and origin, with their timestamps |
| Aborts | lifecycle events | why runs ended — turn limit, error, budget, kill |
| Work | `recording_events`, `kind='action'` | which actions were executed, with `ok`/failed |
| Indicators | `KPIS.md` evaluated | the agent's own counting rules, with their targets |
| Cost | `cost_entries` | per task, and against the indicators as a unit cost |
| Friction | `approvals`, guard-rail events | rejected proposals, attempts at forbidden actions |
| Standing findings | the config lint | what the mechanical rules already say about this config |
| Stuck | `blocked` tasks without resumption | the failure mode nobody sees, because nothing errors |

None of this is free text produced by the agent or by a target system — with **one honest exception**: task titles, which frequently come from the wake source and can therefore carry a ticket subject. It is one line per task rather than a thread, and the file cannot be read without it, so it stays and is named here rather than discovered later.

**The recording is reachable, but through the gate.** Where the file says "eleven runs died at the turn limit" and the question is *why*, the feedback agent asks for one specific run — and that request goes through the approval gate that already exists ([`06-observability-control.md`](06-observability-control.md)): a human sees which run, for which agent, out of which review, and approves once. The approval is one-time consumable (`approvals.used`), so the answer is one recording and not a licence.

This is the one place where the design costs real work rather than composition. The `covey/` meta actions do not know the approval path: where a target-system action creates an approval, returns `pending` with a correlation key and lets the task go `blocked` until a human decides ([`internal/orchestrator/orchestrator.go`](../internal/orchestrator/orchestrator.go)), the meta actions refuse outright — *"requires an approval and cannot be performed unattended"* ([`internal/orchestrator/hiring.go`](../internal/orchestrator/hiring.go)). That branch has to become real. It is worth building on its own: every future meta action that touches something sensitive inherits it, and a guard-rail type that silently degrades to a prohibition on one class of actions is a governance surface that lies about itself.

## The proposal: a version that is stored and not in effect

[`20-hiring-and-setup.md`](20-hiring-and-setup.md) considered exactly this mechanism and deferred it:

> The gentler variant — *propose* its own config as an unactivated version for a human to accept — is not built. The config versioning has no notion of a version that is stored but not in effect (the latest version is the active one) […] **That is worth doing when a second case for it turns up.**

This is the second case, and it is the better one to build it for: proposals about *other* agents need review far more than an agent's proposal about itself does.

The change is small where it counts. `agent_config_versions` numbers versions per agent and treats the highest as the truth; a proposal is a row that is **not numbered into that sequence** — it carries the agent it is for, the version it was written against, the files it changes, the task it came out of, and a status (`pending`/`accepted`/`rejected`). Accepting it writes a normal new version through the existing path, with the human as `created_by`; rejecting it keeps the row and the reason, because a rejected proposal is the most useful thing a reviewer of the *feedback agent* can read.

Two properties fall out and both are worth having:

- **Proposals are diffs against a base.** A proposal written against version 7 and accepted after the agent was edited to version 9 must not silently overwrite the edit. It is shown as stale and re-based or discarded — the same conflict a pull request has, and the same answer.
- **`20`'s open item closes with it.** Once an inactive version exists, the People department can propose its own configuration after the self-onboarding, which `20` wanted and could not have.

The review surface is the one the platform already uses twice: the diff that the config copilot and the drafted agent are accepted through.

## Rule 4 stays; the new action is strictly weaker

`set_agent_config` reaches exactly the agents drafted in the same assignment — *"A compromised People department cannot rewrite the QA agent's soul"* ([`internal/orchestrator/hiring.go`](../internal/orchestrator/hiring.go), rule 4). The feedback agent's whole job is to reach agents it did not draft, and the tempting move is to widen that rule.

Widening it would be the single most expensive change in this document. Instead the capability arrives as a **second action that cannot do what the first one does**: `propose_agent_config` writes an inactive version and nothing else. Rule 4 stays untouched for `set_agent_config`, and the new action needs no equivalent restriction, because its output does not run. A compromised feedback agent produces a queue of bad proposals that a human declines — which is a nuisance, not an incident.

| New action | What it answers |
|---|---|
| `covey/personnel_file` | the file above, for one colleague and a period |
| `covey/read_recording` | one run in full — through the approval gate |
| `covey/propose_agent_config` | an inactive config version for a colleague, with a rationale |

`get_agent_config` and `org_chart` already exist and are what the agent reads the config and the reporting lines with.

**A second scope, as `20` anticipated.** The platform's own system knows exactly one scope today, `agents:write`, and the file that defines it says what to do when a second case appears: *"a second scope belongs here and in `mayDraftAgents`, and the prompt section has to be narrowed with it — an agent that reads about `create_agent` and is then refused is exactly the capability-by-suggestion this file is built to avoid."* So:

```markdown
- system: covey   scope: agents:review
```

`agents:review` unlocks the three actions above and **not** drafting; `agents:write` unlocks drafting and not these. An agent may hold both, and the People department's two employees deliberately do not: one hires, one develops, and neither can do the other's job with the other's credentials. The prompt section follows the scope, as it does for hiring.

**Guard-rail subjects per action** (`covey:personnel_file`, `covey:read_recording`, `covey:propose_agent_config`), so an organisation can switch off reading colleagues' evidence centrally without editing a config — and so `covey:read_recording` can be set to `require_approval`, which is where the gate above comes from.

## The conversation itself

A review is a task like any other, and it is worth being deliberate about what triggers it, because the trigger decides what the feature costs.

**The heartbeat, per period rather than per agent.** One task per review cycle that names the agents due — not one heartbeat per colleague, which multiplies the population by the cycle and makes the People department the most expensive department in the house. Which colleagues are due comes out of the org chart and the last review's date.

**What the run produces**, in this order, because the order is the judgement:

1. It reads the personnel file and the current config.
2. It decides what kind of problem it is looking at. This is the whole skill of the role, and the three answers are genuinely different: *this agent's configuration is wrong* → a proposal. *This agent's assignment is wrong* → a note to the human who owns it, because the platform does not let an agent redirect a colleague's remit. *The platform is wrong* → an issue, see below.
3. It writes the review. Where it lands is the HR metaphor finishing itself: on the **employee profile** ([`09-enterprise-model.md`](09-enterprise-model.md)), dated, next to the run history and the cost bar that are already there. A personnel file with a review history is what the profile has been missing, and it is the page a person opens when they wonder what is going on with an agent.

**Nothing about it is secret from the reviewed agent** — the profile is visible to whoever may see the agent. An organisation that wants agents not to read their own reviews is welcome to that policy, but it is not this platform's job to build a locked drawer for text about a config file. What must not happen is the review *reaching the agent's prompt*, which is the KPI rule from the first section and is enforced by what a proposal may contain, not by hiding the page.

## The channel from colleagues: nothing to build

Agents can already flag problems to the feedback agent. `covey/create_task` delegates to a colleague by slug within the organisation, and it is fail-closed in the three directions this needs ([`internal/orchestrator/orchestrator.go`](../internal/orchestrator/orchestrator.go)): a depth limit of 3, a width limit of 10 per run, and no second open task with the same title at the same target. The last one is exactly the loop that would otherwise form — an agent that hits the same wall every night files the same complaint every night.

So the channel is a **section in `PLAYBOOKS.md`**, not a mechanism: when you were blocked by something that was not the assignment — a missing action, a tool that is not allowed, a limit you keep hitting — file it with the People department instead of working around it silently. What that buys is the thing the personnel file cannot show: the agent's own account of *why*, written while it still knew.

Two properties of `create_task` are worth watching rather than pre-empting: every report wakes the feedback agent immediately, and every wake is a run that costs money. If the noise turns out to be real, the answer is a report that collects until the next heartbeat rather than dispatching — but that is a new concept next to the backlog, and it should be paid for by observed noise, not by anticipation.

## Issues in the platform's own repository

The most valuable finding a feedback agent makes is the one it cannot fix by editing a config: three agents died at the turn limit this week, and none of them was misconfigured — the platform has no way to hand back a partial result. That is not a personnel matter, it is a bug report, and the agent that noticed it is the only entity in the organisation that saw all three.

Nothing needs building for this: the `gitlab` and `github` plugins can `create_issue`, and the repository already keeps `feature-requests/` for exactly this genre. What it needs is a decision and a discipline.

**The decision is which repository, and it is an organisation's to make.** An instance running against the public GitHub mirror would have an agent filing issues where the world reads them; an instance filing into its own GitLab keeps them in the house. The default is the internal one, and the target is configuration rather than something the agent chooses.

**The discipline is that an issue costs a human's attention**, and an agent that files one per review turns the tracker into noise. Three rules, all of them prompt-level because they are judgement and not safety: it files when the same limit hit **more than one agent**, it looks for an existing issue first, and it names the evidence — which agents, which runs, what it cost. A report that says "the turn limit is too low" is worthless; one that says "eleven runs across three agents ended at the limit, $340, and in nine of them the work was nearly done" is a specification.

## What it may never do

Mirroring the four rules in [`20-hiring-and-setup.md`](20-hiring-and-setup.md), enforced in the control plane and not in a prompt:

1. **It proposes, it does not activate.** There is no path from this agent to a config that runs.
2. **It does not review itself**, and it cannot propose its own configuration — the same line `20` draws, and for the same reason.
3. **It reads facts by default.** A conversation is reachable only through an approval, one run at a time.
4. **Nothing else about a colleague is reachable**: not its secrets, not its guard rails, not its runtime, not its budget, not its kill switch.

**And the rule that closes the loop, as in `20`: there is no `fire` action.** Not a forbidden one — a missing one, so there is nothing to forget to check. An agent may say that a colleague is not working out. Ending the employment is a human's act, the same way starting it is.

## Build order

Each slice is worth shipping on its own:

1. **The inactive config version** — schema, write and read path, the accept/reject surface with the diff. Closes `20`'s open item on its own, before anything else here exists.
2. **The approval path for meta actions** — `pending` + correlation key + resume, instead of the flat refusal. Every future sensitive meta action inherits it.
3. **The personnel file** — as a control-plane query first, visible on the employee profile. Useful to a human reading it directly, before any agent reads it.
4. **The three actions** with the `agents:review` scope and their guard-rail subjects, and the prompt section that follows the scope.
5. **The bundle** — the People department's second employee, with its playbook, its `HEARTBEAT.md` and the review cycle.
6. **The review on the employee profile** — history, dated, linked to the task it came out of.
7. **The colleague channel** — the `PLAYBOOKS.md` section for the rest of the population.
8. **Issues in the platform's repository** — the target as configuration, and the discipline in the playbook.

## Open points

- **The review cycle's cost is the feature's price.** A weekly review across a population of thirty is thirty runs a week with a large reading context, and it competes for the same capacity as the work ([`18-runtimes-capacity.md`](18-runtimes-capacity.md)). Reviewing on a trigger — an agent whose abort rate or unit cost moved — is cheaper and more targeted, but it makes the platform's most judgement-heavy agent fire on the indicators that `17` deliberately keeps out of any control loop. Cycle first, because it is honest about what it costs; the trigger is worth revisiting once there is a population to measure it on.
- **Who may see a personnel file.** `17` already leaves this open for the indicators (*"performance data per agent is more sensitive than a cost total"*). The file is sharper than the indicators, and the answer decides both.
- **Reviews across organisations.** Nothing here is org-scoped by accident — every action resolves within the sender's org, as delegation does. Whether a group of organisations on one instance ever wants a shared People function is a question for [`09-enterprise-model.md`](09-enterprise-model.md), not for this document.

---

**Related:** [`20-hiring-and-setup.md`](20-hiring-and-setup.md) (the People department, the `covey` actions, the draft state) · [`17-kpis.md`](17-kpis.md) (what is countable, and why it is not a control loop) · [`06-observability-control.md`](06-observability-control.md) (recording, approval gates, supervisor agent) · [`02-agent-model.md`](02-agent-model.md) (config as code, the config lint) · [`09-enterprise-model.md`](09-enterprise-model.md) (employee profile, human roles)
