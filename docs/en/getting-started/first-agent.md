---
slug: first-agent
title: Create your first agent
description: 'Five steps from an empty Covey to a working AI agent: set it up, write a brief, look the draft through and hire it, give it a task, watch the run in the recording.'
faq:
  - q: What does an agent run cost?
    a: As much as the tokens the model consumes — Covey itself bills nothing. Every run is recorded with tokens and cost, broken down by agent and model. You can set a budget per agent; once it is reached, the agent stops working.
  - q: What belongs in SOUL.md and what in PLAYBOOKS.md?
    a: '`SOUL.md` holds who the agent is: role, remit, tone, limits. `PLAYBOOKS.md` holds how it proceeds: the sequence of steps for a recurring case. Rule of thumb — the soul rarely changes, a playbook changes as soon as the procedure does.'
  - q: Can I move an agent from another Covey instance?
    a: Yes. A configuration can be exported as a bundle and imported into another instance — playbooks, requested access and heartbeat included. Secrets stay where they were; they belong to the organisation, not to the bundle.
  - q: Why is my agent doing nothing although there is a task in its backlog?
    a: 'The three usual reasons: no credential is deposited (the checklist says so), the sandbox image is missing (the startup log says so), or the agent was stopped with the kill switch. In all three cases the recording names the reason.'
---

# Create your first agent

Creating an agent is an onboarding: identity, role, access, supervisor. The difference from a human is that this time the role description actually gets read — it is the prompt.

Five steps, and the **first steps** checklist on the agent overview ticks them off while you do them. It reads your organisation's real state rather than your progress through a tour — once something stands, it stands.

## 1. Setup

The *Setup* page asks three questions, each one skippable:

- **Engine and credential.** Which engine your agents think on, and the credential for it — for Claude Code an API key (billed per use) or a subscription token (generate it once in the terminal with `claude setup-token`), for Codex an API key or the contents of `~/.codex/auth.json`. The value is checked against the provider **before** it is stored — better here than an hour later inside an agent's run. The workplace is created around it; a second token automatically becomes further capacity.
- **What your company does.** Three to five sentences. They stay on the organisation and from then on go into every hiring brief, into the configuration of newly drafted agents and into the config assistant.
- **Your People department.** An agent whose job is drafting the others.

The page runs on its own, with no navigation next to it, and disappears from the menu once the three cards are done. All of it can also be done by hand later (secrets, runtimes, the template library). What the setup buys is the order: without a credential nothing the interface offers can actually run.

## 2. The brief

*New agent* offers four ways in. The shortest is the **brief**: one free-text field — what should the new colleague do? — plus department and supervisor. That becomes an assignment for the People department, and the interface then shows the hiring conversation as it happens. If the description is too thin, the agent asks back instead of guessing.

The other three ways remain: a **template** from the library, the **manual** form for whoever knows exactly what they want, and the **bundle import** from `examples/` (coding agent, QA agent, web researcher, log triage).

## 3. Look it through and hire it

What comes out is a **draft** — on all four ways in. It sits on the agent overview under *Applications*: created, inspectable, changeable, and not working. No dispatch, no heartbeat, no live webhook, no sandbox, no cost.

Look at its configuration — that is what the state is for:

- `SOUL.md` — the role description: who you are, what you are responsible for, in what tone you answer, where you stop. It is compiled into the prompt on every run, is versioned and can be rolled back with its history.
- `PLAYBOOKS.md` — procedures, step by step
- `CAPABILITIES.md` — what the agent is responsible for and what not
- `ACCESS.md` — the access, in the form `- system: zammad scope: read,write,comment`
- `HEARTBEAT.md` — recurring tasks, e.g. `- alle: 30m titel: Inbox aufgabe: Triage new tickets.`
- `ORG.md` — the supervisor to escalate to

A good first sentence in `SOUL.md` is more concrete than you would think: "You answer questions about the billing system" carries further than "You are a helpful assistant".

The drafts sit on the agent overview in their own **Applications** panel, set apart from the workforce below. **Hire** shows a summary first — role, requested target systems with their scopes, supervisor, runtime, budget cap — and then releases the waiting tasks. If the draft does not fit, **Reject** discards it; it never worked, so there is nothing to clean up.

## 4. First task

Create a task in the agent's backlog. "Wake" starts processing right away instead of waiting for the next trigger. Afterwards the agent is `sleeping` again — that is the normal state, and it costs nothing.

## 5. Watch

The recording shows the run from the inside: every tool call, every target-system action, every approval, screenshots for browser work, and tokens and cost at the end. Whoever wants to know why an agent did something reads it here, not in the model.

## After that: a remit, not a list of chores

An agent that only waits for tasks somebody types is an expensive command-line tool. It gets interesting with a **wake source**: a webhook from the ticket system, a heartbeat every few minutes, an incoming email. Then it works when there is work, and sleeps when there is none.

## Next

- [The agent model](../concepts/agent-model.md) — what technically makes up an agent
- [Target systems & plugins](../integrations/target-systems.md) — connecting Zammad, GitLab, email, Teams
- [Guard-rails & control](../concepts/guard-rails.md) — approvals, kill switch, recording
