---
slug: core-concepts
title: Core concepts
description: 'The platform''s glossary: agent, control plane, data plane, runtime, backlog, guard-rail, secrets broker, workplace and wiki memory — briefly explained.'
faq:
  - q: What is the difference between a runtime and an agent?
    a: The agent is the permanent identity with its configuration, backlog and memory. The runtime is the replaceable machinery that drives the model during a run. The same agent can be moved to a different runtime without losing its history.
  - q: Why does an agent work serially rather than in parallel?
    a: Because parallel runs of the same agent would fight over the same home, the same task and the same state. Throughput comes from more agents, not from more concurrency inside one.
  - q: What happens when a sandbox crashes?
    a: Nothing that cannot be restored. The state lives in the control plane, the home on disk. The sandbox is rebuilt at the next wake; an interrupted task goes back into the backlog.
---

# Core concepts

Twelve terms that keep coming back in the interface, in the specification and on these pages. Read them once and the rest needs no explaining.

## Agent

A configured, permanently existing entity with identity, sandbox, access and backlog — the counterpart to an employee. An agent is not a process that runs: its normal state is `sleeping`. A task, a webhook or a heartbeat wakes it, it works serially, and it goes back to sleep. A dedicated email address is optional.

## Control plane

The central, stateful service — one Go binary: scheduler and dispatcher, agent registry, org chart, backlog store, identity and secrets broker, guard-rail engine, observability. It knows the state of every agent. **The control plane is the product**; sandboxes are commodity, runtimes are swappable.

## Data plane

All the sandboxes where the work actually happens. They are deliberately dumb and replaceable: lose one and it is rebuilt from config and home. Only the agent's home is persistent.

## Runtime and daemon

The **runtime** is the agent framework that drives the model loop — the first adapter is Claude Code headless via `claude -p`. The **daemon** (`coveyd`) is the lean process inside the sandbox that speaks the platform protocol and starts the runtime. The adapter between them is thin, so swapping the runtime never becomes a rebuild of the platform.

## Workplace (runtime contract)

Whose contract an agent works on. A workplace carries the engine and its capacity — a subscription seat, an API key, several of them. The order of the credentials is the merit order: capacity already paid for runs before capacity billed per token. With a single login you notice none of this; the platform creates the workplace itself.

## Backlog

An agent's persistent task list, as an object in its own right rather than a chat history. States: `open`, `in_progress`, `blocked`, `done`, `failed`, `cancelled`. `blocked` is the interesting one: the agent is waiting for an answer — from a human or from a target system — and is woken when it arrives.

## Guard-rail

A centrally defined boundary on agent behaviour: approval required for an action, a forbidden system, a blocked tool, an egress rule. It is enforced **outside the runtime** and fail-closed — an agent cannot talk its way past it, because it is not in its prompt.

## Secrets broker

The service that issues access at runtime, short-lived and scoped. Long-lived secrets do not enter the sandbox. Storage is AES-GCM in Postgres columns, encrypted with the instance's `COVEY_MASTER_KEY`.

## Target system

A connected foreign system — Zammad, Salesforce, Jira, Confluence, GitHub, GitLab, Microsoft Teams, SharePoint, Nextcloud, email, a headless browser, an MCP server. Each is a plugin — compiled, a manifest, a WebAssembly module or an MCP server; the core knows no special cases. Actions go through a proxy so guard-rails and recording apply.

## Wiki memory

What the agent keeps: linked Markdown pages with a `pgvector` index, not a heap of flat snippets. Readable and correctable by hand — you can teach an agent something, and make it forget something specific.

## Config as code

An agent's behaviour lives in files: `SOUL.md` (role and tone), `PLAYBOOKS.md` (procedures), `CAPABILITIES.md` (remit), `ACCESS.md` (access), `HEARTBEAT.md` (recurring tasks), `ORG.md` (supervisor). Versioned, with a history, editable through the interface or the API.

## Recording and kill switch

Every run is recorded: tool calls, target-system actions, approvals, browser screenshots, tokens and cost. The kill switch stops one agent — or the organisation's entire workforce in a single move.

## Next

- [Architecture overview](architecture.md) — how these parts fit together
- [The agent model](../concepts/agent-model.md) — what makes up an agent
- [Backlog & lifecycle](../concepts/backlog-and-lifecycle.md) — the states in detail
