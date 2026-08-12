# 21 — Operations and improvement (why a workforce underperforms)

An agent that is not delivering has three possible causes, and the platform can investigate one of them.

Its **configuration** may be wrong — that one at least has a mechanical check. Its **assignment** may be wrong, and nothing looks at that at all. Or the **platform underneath it** may be wrong, and that is the cause nobody in the organisation is positioned to see: it takes reading several agents' work at once to notice that four of them died the same death for a reason none of them chose.

Today the answer to all three is the same shrug. An agent is drafted, reviewed once by a human, hired ([`20-hiring-and-setup.md`](20-hiring-and-setup.md)) — and from then on its configuration is touched only when something has already gone wrong loudly enough for a person to notice. What that person then reaches for is the config, because it is the only thing in front of them.

Two facts sit unused next to each other:

- **The evidence is complete.** Every action, every state transition, every abort, every cost entry is written by the control plane, outside the runtime, at the moment it happens — and `recording_events` has no retention ([`17-kpis.md`](17-kpis.md)). What an agent did in its first month is still there in its sixth.
- **Nothing reads it as a whole.** The config lint reads a thin mechanical slice ([`internal/agents/lint.go`](../internal/agents/lint.go)) — heartbeats too tight, runs piling up at the turn limit, work that leaves no trace. Those rules were right about a QA agent from day one: 22 of 23 failures at the turn limit, several hundred dollars spent, not one merge request tested through. Nobody ran the check, because nobody runs a subcommand on a hunch.

This document adds the reader: an **improvement engineer** in a department of its own, whose subject is not an employee but the **working conditions of the whole workforce**. It reads what a colleague actually did, works out which of the three causes it is looking at, and acts accordingly — a proposal for the config it may reach, a finding for the human who owns the assignment, an issue against the platform where the fault is the platform's.

## Why this is not the People department

Covey's method is to find the counterpart in a real company rather than invent one ([`README.md`](README.md)), and the counterpart here is the continuous-improvement job, not the HR one: somebody who watches the line, changes the procedure where a procedure is the problem, and files the machine fault where it is not.

The distinction earns its keep immediately. An HR framing forces every finding into the shape *this employee is underperforming* — and half of them are *the workshop is broken*. An agent is not only as good as its configuration; it is as good as the platform underneath it, and a reader who can only ever conclude "your config is wrong" will eventually be wrong about a colleague who was configured perfectly well.

**What stays with the People department is hiring**, and only that: turning a description of a job into a config and drafting an agent is a bounded act that ends when a human signs. **What comes here is everything afterwards, the personnel half included.** Reading a colleague's record and proposing a change to their config lives in this department rather than in that one, because it is the *same act* as reading the platform's record and filing a bug: look at what actually happened, and work out which layer it came from. Splitting those two across departments would mean the run that concludes "this playbook is too vague" has to hand off the half that concludes "and these three all died at the turn limit, which is ours" — to nobody, because nobody else sees both.

## The one rule everything else follows from: it judges, it does not decide

[`17-kpis.md`](17-kpis.md) refuses the control loop on purpose, and the refusal is load-bearing:

> the numbers here are **reporting**, not a control loop: nothing in the platform rewards an agent for its KPI, nothing shows it its target, and no scheduler decision hangs off one. […] The judgement itself stays where it already sits: in the recording, in the approval gates, and in the humans who read both.

An agent that reads indicators and rewrites configurations closes exactly that loop, and Goodhart's law arrives with it. The resolution is not to weaken the sentence but to keep it true: **the improvement engineer's output is never in effect.** It writes a proposal; a human accepts it. Nothing in the platform changes on an indicator alone, no scheduler decision hangs off one, and the last line of `17` still holds — the judgement stays with the humans, who now get it prepared instead of having to assemble it.

Four consequences, and the last is the one that is easy to miss:

1. A proposal is a **stored, inactive configuration version** — see below. There is no path from this agent to a running config.
2. It **does not read its own figures.** The work record of the agent asking for it is refused, for the same reason `KPIS.md` is kept out of the system prompt (rule 4 below): an agent that knows what it is measured on works towards the measure. Its own *proposal* is a different matter and is allowed — see the note under "The proposal" below.
3. **A proposal may go deep, and the depth decides who may accept it.** Nothing here restricts *what* a proposal may change — a role that has drifted belongs in `SOUL.md`, not in a footnote. But `ACCESS.md` and `EGRESS.md` are the text view onto state whose write path is reserved for `platform_admin`/`security` ([`02-agent-model.md`](02-agent-model.md)), and that boundary is inherited rather than bypassed: a proposal that widens a colleague's access cannot be accepted by the team lead who owns the agent. The acceptance surface therefore reads the proposal's files and asks the right person — a review dialog that lets everything through because the *proposal* was harmless would move the access decision from security to whoever clicked first.
4. **It may not quote an agent its own figures.** `KPIS.md` is deliberately not compiled into the system prompt — *"an agent that knows it is measured on the number of comments writes more comments"* ([`internal/agents/kpi.go`](../internal/agents/kpi.go)). A proposed `SOUL.md` containing "you resolved 12 tickets last week, aim for 20" would smuggle the target into the prompt through the back door and undo that decision without anyone noticing. A proposal describes **behaviour and procedure** — "close the partial result before the turn limit, file the rest as a subtask" — never scores. Enforced by review, not by a parser: a human reads the diff, and this rule is what they are reading for.

## The work record: facts, not conversations

The improvement engineer needs to see the work. The obvious implementation — hand it the recordings — is the wrong default in three ways at once: recordings carry ticket and mail content from other departments, so one agent would become an exfiltration path across the whole org chart; text from a target system reaching the agent that proposes configurations is the injection path from [`04-identity-secrets.md`](04-identity-secrets.md) pointed at the most valuable target on the platform; and a month of recordings does not fit in a context window at any sensible price.

So the default is a **work record the control plane assembles**: facts it recorded itself, per agent and period.

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

None of this is free text produced by the agent or by a target system — with **two honest exceptions**, both a single line rather than a thread:

- **Task titles**, which frequently come from the wake source and can therefore carry a ticket subject.
- **The question a stuck task is waiting on**, which the reviewed agent wrote itself in its `covey/block` directive and which routinely quotes what it is waiting for — a customer's reply, a ticket. Without it the section that matters most degrades to "something is blocked": *"waiting for an event"* is an observation, *"waiting on whether the customer comes back"* is a finding.

Both stay, because the record cannot be read without them, and both are named here rather than discovered later. What keeps them from being an instruction to the reader is not a parser but the shape of the role: the improvement engineer is told in its prompt that this text is quotation, and its output does not run — a human signs every proposal. A record that promised "facts only" while carrying two free-text fields would be believed at exactly the point where it should be checked.

**Who may read it** is decided here rather than inherited, which is what [`17-kpis.md`](17-kpis.md) asks for when it says performance data per agent is more sensitive than a cost total. The record follows **the recordings, not the cost figures**: `platform_admin`, `security`, the agent's own owner, and `auditor` reading. Costs are visible more widely because a total says what was spent; a work record says how somebody worked, and "whoever may see the bill may see this" is the answer that would make it unusable in any organisation with a works council. The same boundary then covers the indicators that `17` left open, because reading them out of the record is the same act as reading the record.

**The recording is reachable, but through the gate.** Where the record says "eleven runs died at the turn limit" and the question is *why*, the improvement engineer asks for one specific run — and that request goes through the approval gate that already exists ([`06-observability-control.md`](06-observability-control.md)): a human sees which run, for which agent, out of which review, and approves once. The approval is one-time consumable (`approvals.used`), so the answer is one recording and not a licence.

This is the one place where the design costs real work rather than composition. The `covey/` meta actions do not know the approval path: where a target-system action creates an approval, returns `pending` with a correlation key and lets the task go `blocked` until a human decides ([`internal/orchestrator/orchestrator.go`](../internal/orchestrator/orchestrator.go)), the meta actions refuse outright — *"requires an approval and cannot be performed unattended"* ([`internal/orchestrator/hiring.go`](../internal/orchestrator/hiring.go)). That branch has to become real. It is worth building on its own: every future meta action that touches something sensitive inherits it, and a guard-rail type that silently degrades to a prohibition on one class of actions is a governance surface that lies about itself.

## The proposal: a version that is stored and not in effect

[`20-hiring-and-setup.md`](20-hiring-and-setup.md) considered exactly this mechanism and deferred it:

> The gentler variant — *propose* its own config as an unactivated version for a human to accept — is not built. The config versioning has no notion of a version that is stored but not in effect (the latest version is the active one) […] **That is worth doing when a second case for it turns up.**

This is the second case, and it is the better one to build it for: proposals about *other* agents need review far more than an agent's proposal about itself does.

The change is small where it counts. `agent_config_versions` numbers versions per agent and treats the highest as the truth; a proposal is a row that is **not numbered into that sequence** — it carries the agent it is for, the version it was written against, the files it changes, the task it came out of, and a status (`pending`/`accepted`/`rejected`). Accepting it writes a normal new version through the existing path, with the human as `created_by`; rejecting it keeps the row and the reason, because a rejected proposal is the most useful thing somebody checking the improvement engineer can read.

Two properties fall out and both are worth having:

- **Proposals are diffs against a base.** A proposal written against version 7 and accepted after the agent was edited to version 9 must not silently overwrite the edit. It is shown as stale and re-based or discarded — the same conflict a pull request has, and the same answer.
- **`20`'s open item closes with it.** Once an inactive version exists, the People department can propose its own configuration after the self-onboarding, which `20` wanted and could not have. It needs no review scope for that: `agents:write` reaches `propose_agent_config` for the caller's **own** configuration and for nothing else. Proposing for a colleague is what `agents:review` is for, and the separation of the two scopes survives.

  The first draft of this document forbade the self-proposal along with the self-review. That was one rule too many, and the reason it fell is the reason the whole mechanism is defensible: a proposal does not run. What a self-review would buy an agent is a private opinion about its own numbers; what a self-proposal buys it is a sentence a human reads and signs. Only the first is the door this platform keeps shut.

The review surface is the one the platform already uses twice: the diff that the config copilot and the drafted agent are accepted through.

## Rule 4 stays; the new action is strictly weaker

`set_agent_config` reaches exactly the agents drafted in the same assignment — *"A compromised People department cannot rewrite the QA agent's soul"* ([`internal/orchestrator/hiring.go`](../internal/orchestrator/hiring.go), rule 4). This agent's whole job is to reach agents it did not draft, and the tempting move is to widen that rule.

Widening it would be the single most expensive change in this document. Instead the capability arrives as a **second action that cannot do what the first one does**: `propose_agent_config` writes an inactive version and nothing else. Rule 4 stays untouched for `set_agent_config`, and the new action needs no equivalent restriction, because its output does not run. A compromised improvement engineer produces a queue of bad proposals that a human declines — which is a nuisance, not an incident.

| New action | What it answers |
|---|---|
| `covey/work_record` | the record above, for one colleague and a period |
| `covey/read_recording` | one run in full — through the approval gate |
| `covey/propose_agent_config` | an inactive config version for a colleague, with a rationale |

`get_agent_config` and `org_chart` already exist and are what the agent reads the config and the reporting lines with.

**A second scope, as `20` anticipated.** The platform's own system knows exactly one scope today, `agents:write`, and the file that defines it says what to do when a second case appears: *"a second scope belongs here and in `mayDraftAgents`, and the prompt section has to be narrowed with it — an agent that reads about `create_agent` and is then refused is exactly the capability-by-suggestion this file is built to avoid."* So:

```markdown
- system: covey   scope: agents:review
```

`agents:review` unlocks the three actions above and **not** drafting; `agents:write` unlocks drafting and not these. An agent may hold both, and these two deliberately do not: the People department hires, the improvement engineer reads and proposes, and neither can do the other's job with the other's credentials. The prompt section follows the scope, as it does for hiring.

**Guard-rail subjects per action** (`covey:work_record`, `covey:read_recording`, `covey:propose_agent_config`), so an organisation can switch off reading colleagues' evidence centrally without editing a config — and so `covey:read_recording` can be set to `require_approval`, which is where the gate above comes from.

## The review itself

A review is a task like any other, and it is worth being deliberate about what triggers it, because the trigger decides what the feature costs.

**The heartbeat, per period rather than per agent.** One task per review cycle that names the agents due — not one heartbeat per colleague, which multiplies the population by the cycle and makes this the most expensive department in the house. Which colleagues are due comes out of the org chart and the last review's date.

**The cycle is weekly**, written as `alle: 7d`. Worth knowing what that is and is not: `HEARTBEAT.md` has an interval form and a time-of-day form and no weekday ([`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)), so "Mondays at eight" cannot be said. A seven-day interval measures from the last firing and therefore drifts — after a few restarts the review lands on a Saturday night, and its findings wait for a person until Monday anyway. That is tolerable for a cycle whose whole point is regularity rather than punctuality. If it becomes annoying, the fix is a `wöchentlich: Mo 08:00` form in the parser, which is a small addition and useful well beyond this feature — not something this document needs first.

**What the run produces**, in this order, because the order is the judgement:

1. It reads the work record and the current config.
2. It decides which of the three causes it is looking at. This is the whole skill of the role: *the configuration is wrong* → a proposal. *The assignment is wrong* → a finding addressed to the human who owns the agent, because the platform does not let an agent redirect a colleague's remit. *The platform is wrong* → an issue, see below.
3. It writes the review. It lands on the **employee profile** ([`09-enterprise-model.md`](09-enterprise-model.md)), dated, next to the run history and the cost bar that are already there — a record with a history rather than a figure without one, and the page a person opens when they wonder what is going on with an agent.

**The reviewed agent does not see any of this, and that is structural rather than a policy.** The question is worth answering precisely, because the wrong answer would be a quiet defect: an agent that reads an unaccepted judgement about itself is being steered by a change nobody made. Three independent properties keep it out:

- **The prompt carries the active version only.** It is assembled at dispatch from the config files plus the current platform part ([`02-agent-model.md`](02-agent-model.md)). A proposal is not a version, so there is no assembly step it could enter.
- **The review lives on a page.** The employee profile is a human surface; nothing materialises it into a sandbox.
- **Memory is scoped to the agent itself.** The wiki actions resolve against the calling agent's own pages ([`internal/orchestrator/orchestrator.go`](../internal/orchestrator/orchestrator.go)), so the improvement engineer cannot write into a colleague's memory even if a prompt told it to.

The rule to state is therefore not who may hide the page but the one those three properties already enforce: **an open proposal reaches the reviewed agent by no path at all** — not through the prompt, not through a task body, not through memory. One future path has to be closed before it opens: if the shared organisation-wide memory layer from [`05-memory.md`](05-memory.md) (D5) ever exists, a review filed there would be retrievable by its subject. Reviews stay out of it.

Once a proposal is **accepted** it is an ordinary config version and the agent reads it like any other — as a changed instruction, not as a verdict. That is the difference the KPI rule in the first section protects: the agent learns what to do differently, never how it scored.

### How it reaches a human: the list, not a notification

The three outcomes above have one thing in common — each needs a person, and two of them are not proposals. The temptation is to build a way to *tell* somebody, and that is the wrong shape: the platform has no notion of a message to a human, and inventing one for this feature would produce a second, worse inbox next to the one that already has to exist.

Because slice 1 needs an accept/reject surface anyway, that surface **is** the channel: a list of open items per agent owner, carrying all three outcomes — proposals with their diff, findings without one, and issues already filed. It is the shape the approval gates already use, with a filter and a count on it. A finding that only a person can act on is then not a message that can be missed but an open item that stays open.

**Email is a notifier on top of it, never the channel itself.** Where the improvement engineer has an address ([`02-agent-model.md`](02-agent-model.md) — optional, not mandatory), it can tell an owner there is something waiting and link to it. Building the reverse would make the department's core function depend on optional infrastructure, and it would put the content somewhere a proposal cannot be accepted.

## Who checks the improvement engineer

Nobody on the platform, and that is the answer rather than a gap: **a human does**, the same one who accepts or declines everything it produces. The rejection rate on its own proposals is the figure that says whether it is any good, and it sits in its own work record like anyone else's.

That is enough because of the shape of the role, not because the risk is small. An improvement engineer that drifts produces proposals a person declines — visible, cheap, and self-limiting at exactly the rate a human reads. The alternative, a second agent checking the first, buys an extra opinion and one more agent with the widest read access on the platform, which is the trade this whole document has been declining. Written down here so that a later reader does not fill the gap by reflex.

## The channel from colleagues: nothing to build

Agents can already flag problems to it. `covey/create_task` delegates to a colleague by slug within the organisation, and it is fail-closed in the three directions this needs ([`internal/orchestrator/orchestrator.go`](../internal/orchestrator/orchestrator.go)): a depth limit of 3, a width limit of 10 per run, and no second open task with the same title at the same target. The last one is exactly the loop that would otherwise form — an agent that hits the same wall every night files the same complaint every night.

So the channel is a **section in `PLAYBOOKS.md`**, not a mechanism: when you were blocked by something that was not the assignment — a missing action, a tool that is not allowed, a limit you keep hitting — report it instead of working around it silently. What that buys is the thing the work record cannot show: the agent's own account of *why*, written while it still knew. It is also the only one of the three causes the evidence is genuinely poor at, because an assignment that is wrong produces runs that look ordinary.

Two properties of `create_task` are worth watching rather than pre-empting: every report wakes the improvement engineer immediately, and every wake is a run that costs money. If the noise turns out to be real, the answer is a report that collects until the next heartbeat rather than dispatching — but that is a new concept next to the backlog, and it should be paid for by observed noise, not by anticipation.

## Issues in the platform's own repository

The most valuable finding it makes is the one it cannot fix by editing a config: three agents died at the turn limit this week, and none of them was misconfigured — the platform has no way to hand back a partial result. That is a bug report, and this agent is the only entity in the organisation that saw all three.

Nothing needs building for it: the `gitlab` and `github` plugins can `create_issue`, and the repository already keeps `feature-requests/` for exactly this genre. What it needs is a decision and a discipline.

**The decision is which repository, and it is an organisation's to make.** An instance running against the public GitHub mirror would have an agent filing issues where the world reads them; an instance filing into its own GitLab keeps them in the house. The default is the internal one, and the target is configuration rather than something the agent chooses.

**The discipline is that an issue costs a human's attention**, and an agent that files one per review turns the tracker into noise. Three rules, all of them prompt-level because they are judgement and not safety: it files when the same limit hit **more than one agent**, it looks for an existing issue first, and it names the evidence — which agents, which runs, what it cost. A report that says "the turn limit is too low" is worthless; one that says "eleven runs across three agents ended at the limit, $340, and in nine of them the work was nearly done" is a specification.

### It reads the source too, and that is what makes the report worth reading

An agent that may only *write* issues reports symptoms. Give it the platform's own repository to **read** and the same finding arrives as a diagnosis: not "runs die at the turn limit" but "runs die at the turn limit because there is no way to hand back a partial result — `covey/create_task` would be the way and refuses at `maxAgentTaskDepth`, which is exactly this case." The evidence for the first half is in the work record; the second half needs the code, and nobody else in the organisation is holding both.

This is the point where the department's name earns itself. The three causes are only distinguishable by somebody who can look at all three layers, and the third layer is a repository.

Four things it needs, and none of them is new machinery:

- **Read access, in the ordinary way.** An `ACCESS.md` entry on the same target system the issues go to, scoped to reading the code and searching issues. The checkout mechanism is the existing one, and the working copy counts against the agent's checkout budget like any other ([`internal/target/repos.go`](../internal/target/repos.go) — five kept per agent by default, least recently used dropped).
- **Pinned to the commit the instance is running.** `internal/buildinfo` carries version and commit, and the startup log and the interface's footer already show them. An agent reading `main` reports against code this instance does not execute — half of those findings are already fixed and the other half are not there yet, and both kinds cost a maintainer the same read. The running commit is the anchor, and it is available without asking anybody.
- **The same repository the issues go to**, from the same configuration. An organisation on the internal GitLab reads the internal GitLab.
- **A boundary: it reports, it does not fix.** Read access plus a language model invites the patch, and the patch is somebody else's job — the coding agent that already exists as a template (`examples/coding-agent.bundle.json`) picks the issue up. That is the org chart doing what an org chart is for, and it keeps this agent's output what it has been throughout: a proposal for a human, in the tracker where such proposals are decided.

**One risk, named rather than left to be worked out.** An agent reading the control plane's source sees how the guard rails are implemented. That is not a new exposure: guard rails are enforced *outside* the runtime, at the broker, the egress and the tool layer ([`06-observability-control.md`](06-observability-control.md)), the security model has never rested on an agent not knowing how they work, and the source is public on GitHub in any case. What would be a genuine change is write access to that repository — which is why this entry is read-only and the issue is the only thing it produces.

## What it may never do

Mirroring the four rules in [`20-hiring-and-setup.md`](20-hiring-and-setup.md), enforced in the control plane and not in a prompt:

1. **It proposes, it does not activate.** There is no path from this agent to a config that runs.
2. **It does not read its own work record.** Its own configuration it may propose like anybody else's — nothing about that is in effect until a human accepts it, which is exactly the argument that made the inactive version safe in the first place.
3. **It reads facts by default.** A conversation is reachable only through an approval, one run at a time.
4. **Nothing else about a colleague is reachable**: not its secrets, not its guard rails, not its runtime, not its budget, not its kill switch.

**And the rule that closes the loop, as in `20`: there is no `fire` action.** Not a forbidden one — a missing one, so there is nothing to forget to check. This agent may say that a colleague is not working out. Ending the employment is a human's act, the same way starting it is.

## Build order

Each slice is worth shipping on its own:

1. **The inactive config version** — schema, write and read path, and the accept/reject surface as a **list per agent owner**, with the diff and the role check on the files it touches. That list is also the channel, so it is not a detail of this slice but its point. Closes `20`'s open item on its own, before anything else here exists.
2. **The approval path for meta actions** — `pending` + correlation key + resume, instead of the flat refusal. Every future sensitive meta action inherits it.
3. **The work record** — as a control-plane query first, visible on the employee profile. Useful to a human reading it directly, before any agent reads it.
4. **The three actions** with the `agents:review` scope and their guard-rail subjects, and the prompt section that follows the scope.
5. **The bundle** — the department and its agent, with the playbook, the `HEARTBEAT.md` and the review cycle.
6. **The review on the employee profile** — history, dated, linked to the task it came out of.
7. **The colleague channel** — the `PLAYBOOKS.md` section for the rest of the population.
8. **The platform's own repository** — read access pinned to the running commit, the issue target as configuration, and the discipline in the playbook. Read before write: the checkout alone already sharpens every finding the earlier slices produce, and it is the half that needs no tracker.

## Open points

**The review cycle's cost is the feature's price.** It is settled for now — **weekly**, on the heartbeat, as described above — and the reason it can be settled so lightly is that the setting is a line in a config file. What is not settled is the shape it takes when the population grows: a weekly review across thirty agents is thirty runs a week with a large reading context, competing for the same capacity as the work itself ([`18-runtimes-capacity.md`](18-runtimes-capacity.md)). Reviewing on a **trigger** instead — an agent whose abort rate or unit cost moved — is cheaper and better aimed.

The argument against the trigger is narrower than it first looks, and worth stating precisely rather than hiding behind a rule. It violates the *letter* of [`17-kpis.md`](17-kpis.md) — "no scheduler decision hangs off one" — but not its *purpose*, which is that no agent has anything to gain from its own numbers. The reviewed agent never sees them, so there is nothing to play towards; what changes is only which colleague gets read this week. That is a defensible reading, and it is exactly the kind of reading that should be written down before it is acted on rather than discovered in a diff afterwards.

So: **the cycle now, the trigger as the open question** — to be reopened at the point where the cycle stops being affordable, which is a fact about a real instance rather than something to decide here.

*Settled while writing:* who may read a work record (with the recordings, above); whether reviews cross organisations (they do not — every action resolves within the sender's org, as delegation does); and who checks the improvement engineer (a human, deliberately — see above).

---

**Related:** [`20-hiring-and-setup.md`](20-hiring-and-setup.md) (the People department, the `covey` actions, the draft state) · [`17-kpis.md`](17-kpis.md) (what is countable, and why it is not a control loop) · [`06-observability-control.md`](06-observability-control.md) (recording, approval gates, supervisor agent) · [`02-agent-model.md`](02-agent-model.md) (config as code, the config lint) · [`09-enterprise-model.md`](09-enterprise-model.md) (employee profile, human roles)
