---
slug: architecture
title: Architecture overview
description: The control plane as one Go binary, a data plane of ephemeral Docker sandboxes, a WebSocket protocol between them. Postgres carries state, queue and vector search.
faq:
  - q: Does Covey need Redis, RabbitMQ or Kafka?
    a: No. The queue is `SELECT … FOR UPDATE SKIP LOCKED`, the notification is `LISTEN/NOTIFY`, the vector search is `pgvector` — all in the same Postgres instance. A broker would be a second service with its own failure modes for something the database already does.
  - q: Can I use a runtime other than Claude Code?
    a: 'Structurally, yes: runtimes hang off a registry as plugins and speak the daemon protocol through a thin adapter. What ships today is Claude Code headless, plus a mock runtime for tests and demos without model cost.'
  - q: Why does the sandbox run in Docker rather than as a subprocess?
    a: Because process isolation is not isolation once an agent executes tools. The container gives you namespaces, its own filesystem and a controllable way out to the network. There used to be a local provider; it was removed so nobody goes to production without isolation by accident.
  - q: How does Covey scale beyond one machine?
    a: 'Through runners: standalone processes that register with the control plane and execute sandboxes. There is still one control plane, the state stays in Postgres, and the homes live centrally.'
---

# Architecture overview

Covey falls into two halves that behave very differently: a **control plane** that holds the state and is always running, and a **data plane** of sandboxes that come and go. Between them sits a protocol, and that seam is exactly what keeps the platform replaceable with respect to the runtime.

## Control plane — one process, one binary

A single Go binary unites scheduler and dispatcher, agent registry and org chart, backlog store, identity and secrets broker, guard-rail engine, observability and the HTTP API. The React interface is compiled in via `//go:embed`, and so are the SQL migrations; at startup the instance migrates itself, guarded by a `pg_advisory_lock`.

The reason for the single process is not frugality but operability: whoever installs Covey should copy one file and start it. Everything that would otherwise stand next to it as a second service — queue, pub/sub, vector index — lives in Postgres.

## Data plane — dumb and replaceable

For each wake the control plane starts a container from the sandbox image, running `coveyd` inside. The container inherits nothing from the host's environment: what it sees is what the control plane put there. The only persistent part is the agent's home, mounted as a volume.

From that follows the operating rule: lose a sandbox and it is rebuilt from config and home. An agent that gets a fresh container on its next run loses nothing that belongs to it.

## Daemon protocol — the stable seam

A bidirectional protocol over WebSocket runs between control plane and sandbox. It carries the assignment in, tool calls and results out, approval requests in both directions, and at the end tokens and cost.

The runtime behind it is swappable because it does not know the protocol — a thin adapter sits in between. The first one is Claude Code headless (`claude -p`); another changes nothing about the platform as long as it speaks the same messages.

## Why the sandbox is a sibling container

The control plane starts sandboxes through the host's Docker socket — so they run **next to** it, not inside it. That has one practical consequence people trip over when adapting the compose setup: an agent home's `-v` path is resolved by the **host's** Docker daemon. The data directory therefore has to carry the same path on host and container; a named volume would point at nothing.

## Postgres as the anchor

One data store carries almost everything:

- **State** — agents, organisations, roles, configuration versions
- **Queue** — `SELECT … FOR UPDATE SKIP LOCKED` instead of a broker
- **Pub/sub** — `LISTEN/NOTIFY` wakes the dispatcher without anybody polling
- **Memory** — `pgvector` for search across the agents' wiki
- **Secrets** — AES-GCM-encrypted columns, bound to the organisation

## Batteries included, but swappable

Every capability has a built-in, database-backed default and a narrow interface for an external provider: `IdentityProvider` builtin (JWT/Argon2id) ↔ OIDC, `SecretStore` builtin (AES-GCM) ↔ Vault. Normal operation runs with `builtin` everywhere — Keycloak, Vault and Redis are options, never prerequisites.

## Distributed data plane

When one machine is no longer enough, **runners** register with the control plane and take over sandboxes; the homes then live centrally. The structure stays the same, only the location of the containers changes.

## Next

- [Operations & deployment](../operations/operations.md) — ports, environment variables, egress, updates
- [Identity & secrets](../concepts/identity-and-secrets.md) — what may enter the sandbox and what may not
- [Target systems & plugins](../integrations/target-systems.md) — how an agent reaches foreign systems
