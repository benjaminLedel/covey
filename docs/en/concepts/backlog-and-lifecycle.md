---
slug: backlog-and-lifecycle
title: Backlog & lifecycle
description: 'How work reaches an agent: the backlog as an object of its own, wake sources via webhook and heartbeat, and the states open, in_progress, blocked, done.'
faq:
  - q: How long can a task stay blocked?
    a: Indefinitely, and with no running cost. The agent sleeps; only the matching event — a customer reply, a comment, an approval — wakes that particular task again.
  - q: What happens if a run breaks off mid-task?
    a: The task goes back into the backlog and is picked up again at the next wake. When the control plane starts, orphaned tasks are collected so nothing is left behind just because a process restarted.
  - q: Can I create tasks from outside?
    a: 'Yes, through the API or through a target system: a ticket, an email or an issue can create a task. That is in fact the normal case — hand-written tasks are the exception.'
---

# Backlog & lifecycle

In covey, work does not arrive through a chat window but through a backlog. That is not a matter of taste: a task that is an object of its own has a state, an owner, a history and an end — a chat history has none of those.

## The states

- `open` — created, waiting to be worked on
- `in_progress` — the agent is working on it right now
- `blocked` — waiting for an answer from outside
- `done`, `failed`, `cancelled` — finished, failed, withdrawn

On the board the columns are freely configurable; underneath, these states remain.

## The run

An agent is woken by a new task, a webhook, a heartbeat or by hand. Then the same chain always follows: sandbox up → pick up the task (looking things up in memory) → work → record the result and file it into memory → sandbox down, agent asleep.

The chain is deliberately short. Anything that takes longer than a run is not solved by waiting but by `blocked`.

## Why blocked is the interesting state

An agent waiting for a customer's reply must not burn compute — and must not miss the reply either. So the task is parked and given a correlation key, for example `zammad:ticket:4711`.

When an event with the same key arrives later, it wakes exactly that task again, with its context. Minutes or three days may pass between question and answer; the cost of waiting is zero.

## Wake sources

- **Webhook** — the target system calls covey when something happens there. The fastest route, and the only one without idle polling.
- **Heartbeat** — a schedule from `HEARTBEAT.md`, e.g. `- alle: 30m titel: Inbox aufgabe: Triage new tickets.` With `nur-wenn:` the control plane cheaply checks first whether there is anything to do at all, and otherwise lets the agent sleep.
- **By hand** — "Wake" in the interface, or an API call.

## Messages in the team chat

A message to an agent in the team chat does not have to become a task. With **Organization → Answer or task** switched on, a cheap turn in the control plane — no sandbox, no target system, only the org chart, the agent's own backlog and the conversation — decides: answer on the spot ("did that go out yesterday?", a greeting, a thank-you), add a note to a task that is already running, or open a task for real work. In a group the agent only takes messages that mention it.

A task opened from a conversation is told that its result is read out there, so the run writes the answer itself rather than a summary for the record; a second cheap turn retells it in a few sentences of chat, and the full result stays one tap away.

How agents talk there — `du` or `Sie`, casual or formal, how many emoji, plus a line of free text — is set on the voice (**Voices → In the team chat**) and, as the default for everything a voice leaves open, under **Organization → Tone in the team chat**. It acts in these two turns only, not in what a run writes into a target system.

## Turn limit and budget

A run has an upper bound on steps (`max_turns`). When it is reached the run aborts in a controlled way instead of going in circles — usually a sign the task was cut too large. Frequent aborts of this kind are also reported by the configuration lint.

## Next

- [Target systems & plugins](../integrations/target-systems.md) — where wake events come from
- [The agent model](agent-model.md) — what remains between two runs
- [Guard-rails & control](guard-rails.md) — where a run stops
