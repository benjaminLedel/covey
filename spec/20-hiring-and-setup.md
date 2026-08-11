# 20 — Setup & hiring (the first credential, the company description, the People department)

Two questions that look unrelated turn out to be the same one.

**The first is setup.** An empty instance asks a person to create an agent before it asks for the credential that would let one run. The checklist in the interface has the right order ([`11-mvp-plan.md`](11-mvp-plan.md) acceptance path: credential → agent → config → task → run), but nothing acts on it: the credential step is a link, and the agent form opens regardless. Whoever follows the buttons instead of the list ends up with a configured agent that cannot think.

**The second is the agent form itself.** It asks a person for a role, a remit, target systems and scopes — four questions that presuppose knowing what the platform can do. Somebody setting Covey up for the first time is precisely the person who does not know that yet.

Both are fixed by the same move: **ask for the credential first, then let the credential do the work.** The token an organisation enters at setup feeds three consumers at once — the sandbox runs ([`18-runtimes-capacity.md`](18-runtimes-capacity.md)), the control plane's own LLM calls (config copilot, dream), and, newly, an agent whose job is hiring other agents. That is the reason it comes first: without it the platform can do nothing for you, and with it, nearly everything that follows can be done *for* you rather than *by* you.

## Setup: three cards, every one skippable

The setup is a page with three cards, not a wizard that holds the door shut. Each has a *Later*, and that is defensible because everything the setup does can also be done by hand afterwards — the secrets and runtimes pages, the template library, the manual agent form. The existing onboarding checklist stays the thread back to whatever was left open; it reports facts about the organisation's state rather than tracking a tour ([`02-agent-model.md`](02-agent-model.md) has the same principle for the config lint: warnings with context, not prohibitions).

**Card 1 — the engine and its credential.** The engine list comes from the engine registry, and the credential fields come from the engine's own declaration — the secret name, the kind (metered or quota), the delivery form ([`18-runtimes-capacity.md`](18-runtimes-capacity.md) § *What an engine declares about its credentials*). Claude Code offers a subscription token or an API key; Codex offers an API key or the contents of `~/.codex/auth.json` ([`19-codex-adapter.md`](19-codex-adapter.md)). A third engine brings its own setup step with it, without this page learning anything about it. Nothing here is a hardcoded list — that is the same rule that makes the target systems plugins.

What the card does on submit, in one transaction:

1. **Check the value.** The mixups are known and cheap to catch: a `sk-ant-oat…` under `anthropic_api_key` is a subscription token filed as an API key, and the reverse happens just as often. On top of the shape check, one live call against the provider. A wrong credential has to fail here, not three clicks later in the first run, where it arrives as a sandbox error nobody can attribute.
2. **Store it as an org secret** under the name the engine declared.
3. **Create a runtime** with that credential in first place. This is the point where the capacity model gets its anchor: from now on there is a contract agents can be assigned to, rather than a loose secret the runtime happens to find.

**Card 2 — "what does this organisation do?"** Three to five sentences of free text, plus the organisation's name. This is the card that carries the rest of the document; see the next section.

**Card 3 — the People department.** A department and one agent in it, whose job is hiring the others. The name is rolled, not typed (the generator is described below), the card shows its `SOUL.md` before it exists, and the agent is created **as a draft** — a state, not a stage prop; see § *The draft state*.

Skipping is safe in both directions. Skip card 1 and cards 2 and 3 still work: the People department is created as a draft and waits for capacity, which is exactly the state a draft is for. Skip cards 2 and 3 and the instance is in the state it is in today, with the checklist pointing at what is missing.

**The setup runs outside the interface's shell** — no sidebar, no help drawer, only the three cards in a reading column with the way out in the corner. That is not decoration: this is the one thing somebody should do on their first visit, and a navigation offering thirteen other places next to it is an invitation to leave. For the same reason the entry disappears from the navigation once all three cards are done; a menu item that is permanently there and permanently finished becomes furniture. Nothing is lost with it — the company description then lives on the org chart, the credential under Secrets and Runtimes, and the page stays reachable by address.

## The company description is master data, not a prompt

The description from card 2 hangs on the **organisation**, next to its name (the `organizations` table carries a name and a kill switch today and nothing else). It is editable afterwards under *Organisations*, like every other piece of org master data ([`09-enterprise-model.md`](09-enterprise-model.md)).

That placement is the whole point. The obvious implementation — a text box that feeds one generation call and is then gone — would answer the question once. As master data the same three sentences answer it in four places:

- in the config of the People department, when it is generated;
- in **every hiring brief** the interface sends afterwards, as the section the hiring agent reads before it drafts anything;
- in the **config copilot's** system prompt, which today describes an agent, its target systems and its guard rails, but not the company it works for ([`internal/httpapi/assist.go`](../internal/httpapi/assist.go), `buildAssistSystem`) — one paragraph, and every proposal it makes knows whose house it is in;
- and, later, as the seed page of the org's shared knowledge.

An organisation that never fills it in loses nothing but the personalisation. An organisation that does fill it in has said once what it does, and does not have to repeat it in every prompt.

## The chicken and the egg: how the first agent comes about

The People department cannot hire itself. Three tiers, each of which stands on its own, and the platform uses the highest one available.

**Tier 1 — the base bundle (always).** A built-in template bundle, like the existing examples in `examples/`, with the organisation's name and description interpolated. Needs no LLM call, works on any engine, works with card 1 skipped, and is available afterwards from the template library for anyone who wants to add the People department later.

**Tier 2 — personalisation during setup (where possible).** A single control-plane call with the credential just entered, which rewrites `SOUL.md`, `CAPABILITIES.md` and `ORG.md` on top of the base for this company. This is the visible payoff of card 2: you type three sentences, press on, and the preview shows a People department that talks about your business — in a second, without a sandbox.

This tier exposes an existing constraint that has to be dealt with rather than worked around. The control plane's LLM access is Anthropic-only (`internal/claudeapi`, resolving `anthropic_api_key` before `claude_code_oauth_token`), and both current callers — config copilot and the dream in [`05-memory.md`](05-memory.md) — sit directly on it. An organisation that sets Covey up with Codex would silently drop to tier 1, and would have no config copilot either. If a second engine is to be a peer rather than a lesser option, the narrow interface belongs in front of these calls that the platform already uses twice elsewhere ("batteries included, but swappable", [`10-architecture-stack.md`](10-architecture-stack.md)): one port, an Anthropic implementation, an OpenAI implementation, and the two existing callers moved onto it. Recorded as an open decision in [`07-open-decisions.md`](07-open-decisions.md).

**Tier 3 — the self-onboarding, and the actual point.** The People department's first task is its own:

> Read the company description. Look at which target systems are connected. Sharpen your own configuration. Then propose the three colleagues this company should hire first.

This runs in the sandbox, so it works on any engine, costs money that is attributed like any other run, and ends with **three draft agents** for a human to review. That turns the end of setup from "you can get started now" into "here are three applications" — and the platform has demonstrated itself once before anyone had to explain it.

One rule constrains it: **the People department may not write its own config.** An agent that quietly rewrites its own `SOUL.md` at night is precisely the door the rest of this platform keeps shut — config as code means a change is somebody's deliberate act ([`02-agent-model.md`](02-agent-model.md)). It is enforced by the same rule that governs everything else it writes: it may configure exactly the agents it drafted in the current assignment, and it did not draft itself.

The gentler variant — *propose* its own config as an unactivated version for a human to accept — is not built. The config versioning has no notion of a version that is stored but not in effect (the latest version is the active one), so it would need a flag through the write and read paths plus a review surface of its own. That is worth doing when a second case for it turns up; until then the self-onboarding sharpens the organisation's picture of the role by drafting *colleagues*, and its own config stays as delivered.

**The second case has turned up.** The department's other role proposes changes to the configurations of colleagues it did not draft, and there the inactive version is not a gentler variant but the only defensible one — see [`21-feedback-and-development.md`](21-feedback-and-development.md). Once it exists, the self-proposal above costs nothing more than allowing it.

## Hiring is a task, not a form

The agent form keeps its first step — display name, slug, engine, with the dice for the name. After that there are two paths.

**The brief (the default).** One free-text field — *what should the new colleague do?* — plus the two structured choices a person can answer without knowing the platform: which department, and who supervises. Submitting creates a **backlog task on the People department**:

```
Title:  Hire: first-level support for the ticket queue
Origin: wizard        Correlation key: hire:<uuid>

## Assignment
<the human's free text>

## The organisation
<the company description from card 2>

## Frame
- Department: Support          - Supervisor: <name>
- Engine: claude-code          - Requested by: <the human>
- Available target systems: zammad, email, gitlab   ← from the registry, not typed
```

The interface then does not show "creating…" but the hiring conversation as it happens: the task's state and its notes over the existing event stream. If the brief is too thin — "make me an agent that does support" — the People department **asks back** through the `blocked` mechanism ([`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)), and the question appears as the next turn in the same window. That is the reason to model this as a task rather than a synchronous call: questions, answers and resumption already have a home here, and an HTTP request would have to invent one badly. When the task completes, the interface shows the drafted agent as a diff to accept, edit or discard — the same gesture the config copilot already uses.

**Filling it in yourself.** The existing four-step form stays, in full. It is the way for somebody who knows exactly what they want, and it is the fallback when the People department cannot run — no data plane, a runtime with nothing free ([`18-runtimes-capacity.md`](18-runtimes-capacity.md) § *Scarcity is a scheduling input*), or the agent still a draft because card 1 was skipped. The interface switches to it automatically and says why. A platform whose only path to a new agent runs through an agent is a platform that cannot be recovered from an empty state.

### Why an agent and not the config copilot

The copilot exists and could do this server-side. It is the wrong tool here, for reasons that are worth naming because they apply to the next feature of this shape as well:

- **It is engine-independent.** It runs wherever the organisation's agents run, including on engines the control plane cannot call directly.
- **It may look things up.** The copilot receives an assembled prompt context; the hiring agent calls the platform's own actions — which target systems are enabled, what existing agents' configs look like, who is in which department — and drafts against what is there instead of against a snapshot somebody remembered to include.
- **Cost, recording, guard rails and budget apply.** An organisation can see what hiring costs, and can forbid parts of it centrally.
- **It may take time and ask back**, without an HTTP request hanging on it.
- **It is the product demonstrating itself.** The HR department for AI agents has an HR department made of AI agents.

Against that, honestly: a sandbox start is seconds to a minute where the copilot answers immediately, and this makes the friendliest path through setup depend on a working data plane at the moment the platform is least likely to have one. Hence the manual path as a first-class fallback, not as an apology.

## `covey` self-service: meta actions, not a plugin

An agent that creates agents needs actions for it. The first sketch made that a target system plugin like any other; building it showed why that is the wrong shelf. **Plugin actions execute in the sandbox** — that is what the plugin interface is for, and what the action proxy does with every `Execute`. These actions have to touch the registry, which lives in the control plane.

The right home already existed: the `covey/…` **meta actions**, where `create_task` and `org_chart` sit. They travel over the daemon protocol, are decided and executed in the control plane, and need neither a credential nor a broker.

| Action | What it answers |
|---|---|
| `covey/list_targets` | which target systems this organisation has connected, with their scopes, and which engines exist |
| `covey/get_agent_config` | a colleague's config — the house style is in there, not in the model's imagination |
| `covey/create_agent` | creates the draft, with job title, department and supervisor |
| `covey/set_agent_config` | writes its config (complete files) |
| `covey/org_chart` | already existed: departments, people, agents |

Deliberately absent: everything to do with secrets, runtimes and guard rails — and, above all, hiring. Access to a target system for a newly drafted agent is **requested** rather than granted: it goes through the approval path that already exists ([`06-observability-control.md`](06-observability-control.md)), so that "who may reach which system" stays a human decision.

Two gates sit in front of all of it. The **access entry** `- system: covey scope: agents:write` in `ACCESS.md` decides whether these actions exist for an agent at all — and only an agent that has it gets the section about them in its prompt, because a capability that is described in the prompt and refused by the control plane is the worst kind. The scope is read, not decoration: an entry without it grants nothing, because a line that gets reviewed like a limit and turns out to be none is worse than no scope at all. There is exactly one scope for hiring, and it covers all four actions — the two reading ones exist to serve the drafting, and nobody has yet needed them on their own. The department's other role brought the second scope, `agents:review`, which unlocks reading a colleague's record and proposing a change to it and grants no drafting at all ([`21-feedback-and-development.md`](21-feedback-and-development.md)); an agent may hold both, and the two employees deliberately do not. On top of that every action carries a **guard-rail subject** (`covey:create_agent` and so on), so an organisation can forbid drafting centrally without touching a config.

Four rules, enforced in the control plane and not in a prompt — the distinction from [`02-agent-model.md`](02-agent-model.md) applies here more than anywhere: a limit that lives in `SOUL.md` is self-binding, and this agent's output is other agents.

1. **What it creates is a draft.** `create_agent` produces an agent that does not run until a human hires it. See the next section.
2. **No self-propagation.** An agent created this way may not carry the `covey` system in its own `ACCESS.md`. Rejected at the action, not discussed in the prompt.
3. **Provenance is written by the platform.** `create_agent` records which task the agent came out of, into the recording. The interface finds the draft through that, instead of trusting a model to hand back an ID it also invented the format for — and because it is the recording and not a process-local note, an assignment that goes `blocked` and resumes hours later still knows its own drafts.
4. **Only its own children.** `set_agent_config` reaches exactly the agents created in the same assignment. A compromised People department cannot rewrite the QA agent's soul — nor its own.

And one property that is not a guard rail but a lesson from the first real run: **`set_agent_config` merges.** A model writes a configuration in two calls — first the character, then the procedures — and a call that replaced the whole set silently deleted the first one. What came out looked complete, had no `SOUL.md`, and the agent reported it as "complete and checked". So what is sent replaces those files and the rest stays; nothing here needs to delete a file. On top of that the platform refuses to save a configuration without a `SOUL.md` at all — the refusal reaches the agent while it can still act, instead of reaching the human afterwards.

## The draft state

An agent that has been drafted but not yet hired needs a real state, not a repurposed one. The kill switch would be technically sufficient — a killed agent does not run — but it would label something that never ran as switched off, and it would confuse two different facts in the same field: *this one was stopped* and *this one has not started yet*.

**The model is a hiring date.** An agent carries the moment it was hired; a draft is one that has none yet. Not a boolean, because the fact has a time: it belongs in the employee profile the same way a human's does ([`09-enterprise-model.md`](09-enterprise-model.md) § *Employee profile*), and it is the field that makes the metaphor and the data model agree. Existing agents are hired as of their creation date — nobody wakes up as a draft.

**What a draft does not do:** it is not dispatched, its heartbeat does not fire, its webhook is not live, it gets no sandbox and it costs nothing ([`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)). Tasks may be queued against it and simply wait — the first assignment is often clear before the contract is signed, and refusing it would force the person to remember it instead.

**What a draft does:** it can be edited, renamed, reconfigured and discarded freely, without the care owed to a running agent. This is where a person tries something out.

**Rejecting it** is the other way out, and it deletes the draft. That is defensible precisely here: nothing ran, so there is no recording, no cost and no trace anybody needs later. What stays is the brief it came out of — that is a task, and the reason for the rejection belongs in it rather than in a tombstone record of an agent that never worked.

**Hiring it** is not a confirmation dialog but a summary: the role, the target systems it asks for with their scopes, the supervisor, the runtime it will be assigned to and its budget ceiling. Confirming sets the hiring date, sends the access requests down the approval path, and wakes the agent for its first task. It is the one point in this whole document where a human unambiguously takes responsibility for a new employee — which is why it looks like a decision and not like a toggle.

**Who else benefits.** The draft state is worth having independently of hiring, and every path that produces an agent goes through it: instantiating from a template, importing a bundle ([`02-agent-model.md`](02-agent-model.md) § *Export & import*), and the manual form, where "finish later" used to produce a half-configured agent that was already live. There is no second way in — an agent that nobody hired has never worked, whichever door it came through.

**And the rule that closes the loop:** there is no `hire` action. Not a forbidden one — a missing one, so there is nothing to forget to check. Drafting is something an agent may do; hiring is something only a person does. Everything else in this design — the review diff, the approval path, the draft that queues its first task and waits — is a variation on that one line.

## The dice move into the binary

The name generator is a frontend module today (German and English pools, virtue surnames and office surnames, several patterns). Setup and the People department now need it server-side. Rather than a second generator that drifts from the first, it moves into the binary and the interface asks for a roll — one source, two consumers. Names stay rolled rather than derived: an agent called `support-agent-2` is a process artefact, and an agent called Renate Büroklammer is a colleague.

## Build order

Cut so that every slice is worth shipping on its own — all eight are built:

1. **The draft state** — hiring date, dispatch, and the *Applications* panel in the interface. Pays off immediately for templates and imports, before any of the rest exists.
2. **The name generator** into the binary, with an endpoint for the interface.
3. **The company description** on the organisation, editable, and one paragraph in the config copilot's system prompt.
4. **The control-plane LLM port**, with the two existing callers moved onto it (D15).
5. **The setup page** — three cards, tiers 1 and 2.
6. **The `covey` self-service actions** with their four rules.
7. **The People department bundle** and the self-onboarding (tier 3).
8. **The brief** — form → task → live conversation → draft review.

## Open points

- **Tier 2 without Anthropic.** Whether the control-plane port gets an OpenAI implementation immediately or Codex organisations live on tier 1 for a while. It decides whether card 2's payoff is visible during setup or only after the first sandbox run. (D15)
- **Where the description ends up in the prompt.** Every agent's system prompt, or only the ones being drafted? It is a paragraph in every run of every agent, and it is exactly the kind of thing that is added once and never measured again.
