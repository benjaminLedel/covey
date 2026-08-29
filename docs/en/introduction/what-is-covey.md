---
slug: what-is-covey
title: What is Covey?
description: 'Covey runs AI agents like employees: own identity, isolated sandbox, brokered access, backlog and org chart. One Go binary, one Postgres database, AGPL-3.0.'
faq:
  - q: Does Covey need a cloud, or an account with you?
    a: 'No. Covey is self-hosted: one binary, one Postgres database, Docker for the sandboxes. The only account you need is with the model provider — an Anthropic API key or a subscription token, both deposited in your own instance.'
  - q: How is Covey different from an agent framework like LangChain or CrewAI?
    a: A framework builds the agent, Covey operates it. Identity, access, backlog, guard-rails, recording and cost are operational questions and live outside the runtime — which is what keeps the runtime replaceable. The first adapter is Claude Code headless.
  - q: What licence is Covey under?
    a: 'AGPL-3.0. You may run it, modify it and pass it on. The one obligation that matters in practice: if you offer a modified version to others over a network, those users must be able to get your modified source under the same terms. Running Covey inside your own organisation triggers none of that.'
  - q: Can an agent work without a target system?
    a: Yes. After installation a demo agent is waiting that needs no connected system — it works on the task text and writes its result back. That is enough to see a full run including recording and cost.
---

# What is Covey?

Covey runs AI agents like employees: every agent has an identity, an isolated workplace, brokered access to target systems, a backlog and a place in the org chart. The guiding metaphor is the IT and HR department for AI agents — and it is meant literally, right down to how the software is built.

Technically, Covey is **one Go binary next to a Postgres database**. The admin interface is compiled in, so are the migrations. No nginx in front, no separate frontend hosting, no message broker on the side. The licence is AGPL-3.0; you run it yourself, on your own machine, with your own keys.

## What Covey is for

For the moment a company runs more than one agent and somebody asks who has actually been watching. That is: who gave this agent that access, what did it do with it last week, what did it cost, and who stops it when it goes wrong.

That is the difference from an agent framework. A framework helps you build the agent. Covey manages the finished agent in operation — which is why the framework stays replaceable: the runtime hangs off a thin adapter, and the first one is [Claude Code headless](architecture.md).

## The organisation is the unit, not the user

Covey belongs to no single person. The unit is the organisation, and everything else follows from that: agents are organisation-owned resources, their secrets live in the organisation's vault, their cost lands on its account, and several people in different roles look at the same workforce — IT, team lead, security, audit, controlling.

This is the load-bearing distinction from single-user apps that promise a personal "AI employee". Their model ends where the second person needs a say.

## Counterparts in the company

Nearly every component has a counterpart everyone knows from working life. If you know the left-hand column, you can read the right-hand one:

- Identity / Active Directory → agent identity, optionally with its own email address
- Workstation / PC → isolated sandbox with a persistent `/home`
- Onboarding / role description → `SOUL.md`, versioned like code
- Password vault / PAM → secrets broker, short-lived tokens at runtime
- Task list / ticket → backlog as an object in its own right
- Operations manual / compliance → central guard-rails, enforced outside the prompt
- SIEM / EDR → session recording, cost per run, an emergency stop for the whole organisation

## What Covey is not

Not a model and not a model vendor — Covey brings your own account (an Anthropic API key or a subscription token). Not a chat window; work arrives from a backlog, a webhook or a heartbeat, not from a conversation. And not a cloud: there is a public instance to look at, but the normal case is your own.

## Next

- [Core concepts](core-concepts.md) — the terms that recur everywhere
- [Architecture overview](architecture.md) — control plane, data plane, daemon protocol
- [Quick start (Docker)](../getting-started/quickstart.md) — the install in five lines
