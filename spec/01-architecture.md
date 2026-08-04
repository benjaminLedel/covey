# 01 — Architecture

## System overview

The system splits into two layers: a central **control plane** that knows and steers the state of every agent, and a **data plane** of isolated sandboxes in which the agents actually work. The control plane runs no agent code — it orchestrates, brokers and observes. The actual work (LLM calls, tool use, file operations) happens exclusively in the data plane.

```
                 ┌─────────────────────────────────────────────┐
                 │               CONTROL PLANE                  │
                 │                                              │
  Admin UI ──────┤  Scheduler / dispatcher                     │
                 │  Agent registry & org chart                 │
  Event router ──┤  Backlog store                              │
     ▲           │  Identity & secrets broker (Keycloak)       │
     │           │  Guard-rail / policy engine (enforcement)   │
     │           │  Observability (recording, alerts, cost)    │
     │           │  Config sync (Git → compiled runtime cfg)   │
     │           └──────────────────┬──────────────────────────┘
     │                              │ Daemon protocol (bidirectional)
     │           ┌──────────────────┴──────────────────────────┐
     │           │                DATA PLANE                    │
     │           │   ┌────────────┐  ┌────────────┐             │
  external       │   │  Sandbox   │  │  Sandbox   │   …         │
  systems  ◀────▶│   │  ┌──────┐  │  │  ┌──────┐  │             │
  (mail,         │   │  │Daemon│  │  │  │Daemon│  │             │
  tickets,       │   │  ├──────┤  │  │  ├──────┤  │             │
  Confluence,    │   │  │Runtime│ │  │  │Runtime│ │             │
  Teams)         │   │  └──────┘  │  │  └──────┘  │             │
                 │   │ persistent │  │ persistent │             │
                 │   │ /home      │  │ /home      │             │
                 │   └────────────┘  └────────────┘             │
                 └─────────────────────────────────────────────┘
```

## Control plane

Stateful and always on. Responsible for:

- **Scheduler / dispatcher** — knows who is asleep, who is blocked, what sits in whose backlog and who goes next. Wakes sandboxes when needed. See [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md).
- **Agent registry & org chart** — master data for all agents, their hierarchy and reporting relationships.
- **Backlog store** — persistent, inspectable task lists (see the lifecycle spec).
- **Identity & secrets broker** — issues agent identities and short-lived, scoped access tokens. See [`04-identity-secrets.md`](04-identity-secrets.md).
- **Guard-rail / policy engine** — holds the central, platform-enforced guard rails and decides every security-relevant request (credential request, egress, tool/action approval) against the policies in force. See [`06-observability-control.md`](06-observability-control.md).
- **Observability** — receives recording streams, evaluates them, raises alerts, caps cost. See [`06-observability-control.md`](06-observability-control.md).
- **Config sync** — reads the agent configuration from Git and compiles a system prompt + runtime config from it (see [`02-agent-model.md`](02-agent-model.md)).

The control plane must be highly available — it is the single point of truth. Its state (registry, backlog, recording metadata) lives in a classic relational DB (PostgreSQL fits), the knowledge-graph memory separately (see [`05-memory.md`](05-memory.md)).

## Data plane

The data plane is deliberately **dumb and replaceable**. A sandbox is an isolated workplace with a persistent home directory and a running daemon. It holds no state critical to the platform beyond the respective agent's working data — if a sandbox is lost, it is rebuilt from config + home.

**Isolation model:** persistent volume + ephemeral compute. The agent "wakes up", mounts its home, works, goes back to sleep. The compute instance itself is short-lived; only the home volume survives.

**Distribution:** the data plane does not have to live on the control plane's host. **Runners** are registered execution nodes modelled on GitLab runners: a process on an arbitrary host registers with a registration token, holds an outbound connection and gets sandboxes assigned to it. This is possible because the data plane never needs to be reachable inbound anyway — the daemon dials out (see the daemon protocol below). The persistent home does not stand in the way: after every job it is synced as a whole into a central, content-addressed **home store** and materialised from there on wake — the same hybrid pattern as the wiki ([`05-memory.md`](05-memory.md)), just without its restriction to curated content. Because deduplication happens block-wise, only changed blocks travel, and the toolchain caches that are identical across all agents are stored once org-wide. A home on a runner is therefore a disposable working copy, and the agent↔runner assignment a scheduling preference rather than a binding. Full model in [`16-runner.md`](16-runner.md).

> **Build vs. buy:** the sandbox infrastructure will **not** be built from scratch. Candidates: Firecracker/gVisor operated in-house (Proxmox experience is available, but ephemeral microVMs are a beast of their own) or sandbox-as-a-service (E2B, Daytona). The differentiated part is the control plane around it, not the container runner. Decision open — see [`07-open-decisions.md`](07-open-decisions.md). The **topology** is unaffected by this: where sandboxes run is decided by D12 (runners), not by the choice of isolation technology — a runner encapsulates exactly that choice per host.

## Runtime abstraction

The central architectural principle. Claude Code, OpenHands, Harness & co. have completely different interfaces (CLI, framework, full harness). Wrapping every runtime directly drowns the platform in special cases.

Instead: the platform manages the **sandbox**, not the framework. A slim **daemon** speaking a uniform protocol runs inside the sandbox. The daemon bootstraps the concrete runtime — one thin **adapter** per runtime. As long as the adapter can start the runtime, feed it a task and pick up its events/outputs, the framework is swappable.

```
Daemon ── adapter(claude-code)  → `claude -p` headless, streams stream-json events (see 12)
       ── adapter(openhands)    → starts an OpenHands session via API
       ── adapter(harness)      → starts a Harness workflow
       ── adapter(custom)       → any LLM loop
```

Adapters are deliberately thin. Their only job is translating between the platform-wide daemon protocol and the runtime specifics.

## Daemon protocol

Bidirectional (WebSocket or gRPC stream) between control plane and daemon. The protocol is the stable seam of the system — runtimes change, the protocol stays.

### Control plane → daemon

| Message | Purpose |
|---|---|
| `wake` | Wake the sandbox, mount the home, bring the daemon up |
| `assign_task` | Hand a task (with backlog ID, context, priority) to the runtime |
| `inject_config` | Set the compiled config (system prompt from `SOUL.md` etc.) |
| `inject_credentials` | Pass a short-lived, scoped token for a target system |
| `approve` / `deny` | Answer to a previously reported approval gate |
| `pause` / `kill` | Stop the agent immediately (kill switch) |
| `sleep` | Task finished/parked → shut the sandbox down |

### Daemon → control plane

| Message | Purpose |
|---|---|
| `ready` | Daemon up, runtime bootstrapped |
| `event` | Runtime event (LLM call, tool call, command) → recording |
| `request_credential` | Agent needs access to a target system → broker |
| `request_approval` | A risky action is waiting for approval |
| `request_org_chart` | Agent queries the org chart (humans & agents including profiles, departments, reporting lines) → answer `inject_org_chart` (see [`09-enterprise-model.md`](09-enterprise-model.md)) |
| `blocked` | Agent parks the task, waits for an external event (with a correlation key) |
| `task_done` | Task finished (with result + memory update). Status `incomplete` reports a run aborted at the turn limit — a handover state instead of a result (see [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)) |
| `request_create_task` | Agent creates a task: a subtask for itself or a delegation to a colleague → answer `inject_create_task` (see [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)) |
| `set_stage` | Agent moves the task into a (possibly new) kanban stage — purely presentational, not a lifecycle transition (see [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)) |
| `note` | A proactive note from the agent mid-run: `scope=task` attaches it to the task, `scope=memory` feeds it into memory immediately (see [`05-memory.md`](05-memory.md)) |
| `cost` | Tokens/compute consumed, for budget tracking |
| `heartbeat` | Sign of life |

The messages `set_stage`, `note`, `request_org_chart` and `request_create_task` originate from the agent's meta actions at the action proxy (`POST /actions/covey/set_stage`, `…/covey/add_note`, `…/covey/remember`, `…/covey/org_chart`, `…/covey/create_task`): the proxy does not treat the system `covey` as an external target system (no credentials, no egress guard rail) but passes them through to the control plane as control signals.

**One exception is `create_task`:** it goes through the guard rails regardless (subject `covey:create_task`, on delegation `covey:create_task:foreign`). The other meta actions merely describe what the agent is doing anyway; creating a task, by contrast, **produces** work, cost and — on delegation — work for someone else. That has to stay governable, and separately so: an organisation can allow an agent to break down its own work while forbidding delegation.

All `event` messages flow into the observability pipeline (see [`06-observability-control.md`](06-observability-control.md)). **Every credential, egress and approval request is checked against the guard rails by the control plane, never decided by the daemon itself.** The daemon additionally enforces the tool/action-side rails locally (which tools/commands are allowed), but the binding policy decision sits centrally — the daemon is the executing organ, not the decision maker.

## External systems

Agents interact with the ticket system, Confluence, Teams, mail etc. **always through brokered, short-lived credentials** — never with hardcoded secrets. Incoming events from those systems (new ticket, mail reply) run through the control plane's **event router** and are mapped there onto agents and, where applicable, parked tasks (event correlation, see the lifecycle spec).
