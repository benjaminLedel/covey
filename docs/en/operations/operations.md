---
slug: operations
title: Operations & deployment
description: 'Running covey: one binary plus Postgres, port 8494, migrations at startup, HTTPS through a reverse proxy, egress isolation, backups and updates without ceremony.'
faq:
  - q: How much memory does a covey server need?
    a: 'The control plane itself is frugal — a Go process next to Postgres. The demand comes from the sandboxes: each runs a Node runtime, plus chromium for browser work. Size by the number of agents awake at once, not by the number created.'
  - q: Can I put covey behind a reverse proxy?
    a: Yes, that is the intended route for HTTPS. The one thing to get right is not setting `COVEY_PUBLIC_URL` to the public domain when the sandboxes cannot reach it — that variable points inwards.
  - q: How do I update without losing data?
    a: Swap the binary or image and restart; migrations run at startup and are guarded against concurrent starts. Back up the database first, and keep the master key anyway. Afterwards run `covey config lint`.
  - q: Does covey run without internet access?
    a: The platform does. The agents need the model endpoint — with hard egress isolation the proxy sits in front and lets exactly the allowed hosts through. A self-hosted embedding service additionally keeps wiki search in the building.
---

# Operations & deployment

covey is deliberately boring to run: one process, one database, one port. Everything beyond that is optional.

## What runs

- **covey** — the control plane, listening on **8494** (API, interface, daemon WebSocket)
- **PostgreSQL** with `pgvector` — state, queue, memory, secrets
- **Docker** — for the sandboxes, started through the host's socket
- optionally **covey-runner** — when the data plane should span several machines

Migrations run automatically at startup, guarded by an advisory lock; two instances starting at once do not migrate against each other.

## Keeping the addresses apart

Two variables look alike and mean opposite things:

- `COVEY_PUBLIC_URL` points **inwards** — the address at which the **sandboxes** reach the control plane. Put the website's domain here and the containers dial back over the open network and fail at the egress allowlist.
- `COVEY_SITE_URL` points **outwards** — the copyable webhook and trigger URLs, the address in the downloadable skill. Leaving it empty is the normal case; the server derives it from the request.

At startup covey warns when these two roles look swapped.

## HTTPS

A reverse proxy in front, TLS terminated there, `COVEY_PUBLIC_URL` and `COVEY_SITE_URL` set accordingly. The secure cookie then switches itself on. For the database, `sslmode=require` or higher.

## Egress isolation

Two levels. **Cooperative**: the sandbox's traffic goes through a proxy that enforces the allowlist. **Hard** (`COVEY_EGRESS_ISOLATION=network`): the sandbox sits on an internal network without internet, and the proxy container is the only way out — no longer bypassable. The hard level needs a second image built.

## Backups

Two things: the Postgres database and the `COVEY_MASTER_KEY`. Without the key a database backup is complete but every secret in it is unreadable. The agents' homes (`COVEY_DATA_DIR`) are useful but reproducible — they hold work in progress, not irreplaceable state.

## Updates

New binary or new image, restart the process; the migrations come along. After an update it is worth a look with

```
covey config lint
```

It changes nothing and reports configurations that no longer sit well with the new version: heartbeat intervals that are too short, blocking tasks on systems without a webhook, boards with columns naming tasks instead of states, frequent turn-limit aborts. The exit code is 1 when there are findings — an upgrade script can react to it.

## Observing

`covey version` answers which build is running; the same information appears in the startup line and at the bottom of the interface. Cost, tokens and runs are in the interface per agent and per model. The request log shows the HTTP edges — what came in, what went out.

## Next

- [Quick start (Docker)](../getting-started/quickstart.md) — the installation
- [Architecture overview](../introduction/architecture.md) — why the sandbox is a sibling container
- [Guard-rails & control](../concepts/guard-rails.md) — kill switch and recording
