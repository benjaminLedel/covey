---
slug: agent-model
title: The agent model
description: 'What makes up a Covey agent: its own identity, an isolated sandbox with a persistent home, versioned behaviour in SOUL.md, and compute only on demand.'
faq:
  - q: Does an agent keep its files between runs?
    a: Yes. The container is ephemeral, the home is not — it is mounted as a volume and survives every restart. A cloned repository, a downloaded file or a note is still there at the next wake.
  - q: Can one agent task another?
    a: 'Through the org chart and the backlog: an agent can create a task for another and wait for its result instead of doing what somebody else is responsible for. Escalation upwards goes to a human.'
  - q: What is a warm sandbox?
    a: A container that stays up between two wakes so the next run does not start cold. Useful for agents woken every few minutes; unnecessary for one that works three times a day.
---

# The agent model

An agent is not a prompt and not a chat history. It is an entity that persists when nobody is looking: with an identity, a workplace, access, memory and behaviour versioned as code.

## Identity

Every agent has its own identity in the platform — not that of the human who created it. Its access hangs off it, so does its place in the org chart and every trace it leaves. Optionally it gets its own email address, and then a target system sees it for what it is: a sender with a name.

That is the difference from a script running under an administrator's account. When somebody later asks who wrote this comment, there is an answer.

## The first day

An agent carries the moment it was hired. Without one it is a **draft**: created, configurable, visible in the org chart — and not working. No dispatch, no heartbeat, no live webhook, no sandbox, no cost. Tasks may still sit in its backlog and start on the first day.

The kill switch would have been technically sufficient, but it would put two different facts in the same field: "this one was stopped" and "this one has not started yet". And hiring is not a flag but a moment in time — it later sits in the employee profile next to a human's.

That is how every agent comes about that nobody wrote by hand: from a template, from a bundle import, and above all where an *agent* drafts the agent. Hiring stays a human act; there is no action for it that an agent could call.

## Sandbox with a persistent home

Work happens in a container that is created for the wake and disappears afterwards. What stays is `/home/agent`: cloned repositories, downloaded attachments, notes, the agent's wiki pages. On the next run it finds its workplace as it left it — the machine around it is new.

The workplace is browsable in the UI: look through it, drop in a template, edit a file, pull a selection out as a ZIP. Also while the agent sleeps, because the home lives on disk and not in the container.

## Behaviour as code

`SOUL.md`, `PLAYBOOKS.md`, `CAPABILITIES.md`, `ACCESS.md`, `HEARTBEAT.md`, `ORG.md` — six files, versioned, with history and rollback. A change in behaviour is therefore something you can read, review and take back, not a deployment.

## Serial before parallel

An agent handles one task at a time. That is a decision, not a limitation: two concurrent runs of the same agent would fight over the same home and the same state, and the resulting bug would not reproduce. Throughput comes from more agents.

## Always reachable, compute only on demand

"Always-on" is a property of the experience, not of the bill. A cheap dispatch loop keeps the agent reachable; the expensive part — container, model — runs only when there is work. An agent in state `sleeping` costs nothing but a row in the database.

For work where startup time matters, a **warm sandbox** can be switched on: the container stays up between two wakes. It costs memory and saves seconds — a deliberate trade-off, not a default.

## Limits the agent cannot move

`max_turns` bounds how many steps a run may take, a budget bounds what it may cost, guard-rails bound what it may do. All three sit outside the model — they are not in the prompt and cannot be argued away.

## Next

- [Backlog & lifecycle](backlog-and-lifecycle.md) — the states and who changes them
- [The wiki memory](memory.md) — what the agent keeps
- [Guard-rails & control](guard-rails.md) — where the platform steps in
