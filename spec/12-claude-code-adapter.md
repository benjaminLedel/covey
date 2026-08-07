# 12 — Claude Code adapter (`-p` / headless)

Makes the runtime adapter from [`01-architecture.md`](01-architecture.md) concrete for the **MVP's first runtime** (M1 in [`11-mvp-plan.md`](11-mvp-plan.md)). Claude Code is driven **headless** through the `-p`/`--print` mode: a prompt in, the full agent loop runs, a result out, an exit code. Exactly the programmatic control the daemon needs — no interactive terminal.

## Basic mechanics

The daemon in the sandbox calls Claude Code as a **subprocess**:

```bash
claude -p "<task>" \
  --output-format stream-json \
  --append-system-prompt "<compiled SOUL.md>" \
  --allowedTools "Read,Edit,Bash" \
  --max-turns 20 \
  --max-budget-usd 0.50
```

`-p` switches from the interactive REPL into a single batch run; all CLI options work with `-p`.

## Flag → Covey concept

The control maps almost 1:1 onto the daemon protocol:

| Claude Code flag | Covey concept |
|---|---|
| `-p "<task>"` | `assign_task` — a task from the backlog ([`03`](03-lifecycle-scheduling.md)) |
| `--append-system-prompt` / `--system-prompt` | `inject_config` — the compiled `SOUL.md` ([`02`](02-agent-model.md)) |
| `--model <id\|alias>` | `inject_config.model` — the model per agent (registry field, PATCH `/agents/{id}/model`); empty = the binary's/account's default |
| `--max-turns <n>` | `inject_config.max_turns` — the turn limit per agent (registry field, PATCH `/agents/{id}/max-turns`); 0 = orchestrator default (30) |
| `--output-format stream-json` | the `event` stream → recording ([`06`](06-observability-control.md)) |
| `--output-format json` → `total_cost_usd` | `cost` → cost control ([`06`](06-observability-control.md)) |
| `--resume <session_id>` | `blocked → working` — resumption ([`03`](03-lifecycle-scheduling.md)) |
| `--allowedTools` / `--permission-mode` | tool guard rails ([`06`](06-observability-control.md)) |
| `--max-turns`, `--max-budget-usd` | budget/runaway guard rails ([`06`](06-observability-control.md)) |
| `--mcp-config` | the action proxy as a tool server — target actions as typed tool calls instead of `curl` in the shell ([`01`](01-architecture.md)) |
| exit code ≠ 0 | error path → `task_done`(error) |

**On `--mcp-config`:** the action proxy serves its actions as MCP tools as well (see [`01-architecture.md`](01-architecture.md)), and the adapter hands the runtime that server. Its tools have to go into the **same** `--allowedTools` list: the flag is a whitelist and may appear only once, and without the entry a headless run would ask for permission on every action — which nobody answers. The route is **opt-in for now** (`COVEY_ACTION_MCP`): the shell form is what every existing agent config describes, and a failed handshake would take all target actions with it. Switch it on per instance, watch one agent's recording for a run, then make it the default.

## `blocked` ↔ session resume (the M4 core)

Headless runs are stateless by default but can be **threaded**: `--output-format json` delivers a `session_id`; a later `claude -p --resume <session_id> "<new input>"` loads the context of the existing run.

That maps exactly onto Covey's `blocked` mechanic:

1. The agent asks a follow-up question → the daemon reports `blocked` with a **correlation key**; in addition the Claude Code run's **`session_id`** is stored on the parked task.
2. The sandbox shuts down (no compute).
3. A correlated event arrives (ticket update) → the daemon starts the sandbox and calls `claude -p --resume <session_id> "<answer>"`.
4. Claude Code restores the conversation context **itself** — Covey does not have to reconstruct it.

With that, the `blocked → working` edge from [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md) is realised with the runtime's own means.

> **Short term vs. long term.** The Claude Code session is the *short-term working context* (a limited context window, possibly expiring). *Durable* memory across tasks stays Covey's memory layer ([`05-memory.md`](05-memory.md)) — ingest at `done`, query at `triage`. Session persistence alone should not be relied on for long-term knowledge.

## Streaming → recording

`--output-format stream-json` emits NDJSON (one JSON event per line, type `assistant` / `tool_use` / `result`). The daemon reads the stream and forwards every line as an `event` message to the control plane → complete **session recording** ([`06-observability-control.md`](06-observability-control.md)) practically for free, including every tool call.

## Cost

The final `result` event (or `--output-format json`) contains `total_cost_usd` including a breakdown per model → straight into Covey's **cost tracking** per agent ([`06-observability-control.md`](06-observability-control.md)), without a separate usage query.

On a **subscription seat that figure is notional** — Claude Code prices the run as if it had been billed, but the seat is paid for regardless. It is booked as it arrives and labelled by the credential it came from; what that means for totals and unit costs is in [`18-runtimes-capacity.md`](18-runtimes-capacity.md) and [`17-kpis.md`](17-kpis.md). An adapter for another engine may not report a dollar figure at all, in which case it has to be derived from tokens and a price list — which is why the token counts are stored separately and not only the money.

## Utilisation

Claude Code can report what its credential has consumed: `claude -p "/usage"` answers **headless and without a model turn** (`num_turns: 0`, `total_cost_usd: 0`, `duration_api_ms: 0`) with the share of the current rolling window and of the week, plus the reset times. That is the provider's own figure, and it beats anything the platform can infer from what it booked itself.

The adapter therefore implements the optional **utilisation capability** described in [`18-runtimes-capacity.md`](18-runtimes-capacity.md). Two constraints come with it:

- The numbers arrive as **prose in the `result` field**, not structured, even under `--output-format json`. Reading them is text matching against a format that will change between versions, so it is handled **fail-open**: an answer that no longer parses leaves the fleet running and falls back to the platform's own estimate. It never blocks.
- The **scope** of the figure needs verifying. Claude Code notes that its contribution breakdown covers only local sessions on that machine; with a fresh sandbox per waking phase that would be a fraction of the credential's real use. Whether the headline window percentages are account-wide is an open point ([`07-open-decisions.md`](07-open-decisions.md)) and decides whether this source carries in Covey at all.

## Auth

Headless needs non-interactive credentials: `ANTHROPIC_API_KEY` as an ENV variable, a long-lived OAuth token via `claude setup-token`, or provider credentials (Bedrock/Vertex/Foundry). In Covey this key is itself a **brokered secret** ([`04-identity-secrets.md`](04-identity-secrets.md)): the daemon gets it injected at runtime, it does not sit permanently in the sandbox.

Which secret and which ENV variable is a property of the **engine**, not of the platform: the name binds the intent (`anthropic_api_key` → `ANTHROPIC_API_KEY`, `claude_code_oauth_token` → `CLAUDE_CODE_OAUTH_TOKEN`; do not guess from the token prefix). The engine declares its credentials in order of precedence, and everything above them — several seats, distribution, limits, cost attribution — follows from [`18-runtimes-capacity.md`](18-runtimes-capacity.md) without the adapter knowing about it.

## Permissions & guard rails

Full non-interactive operation needs `--dangerously-skip-permissions` (it skips the interactive approval prompts). In Covey that is **defensible, because the sandbox is isolated and the hard limits are enforced externally anyway** — at the broker, at the egress, in the tool layer ([`06-observability-control.md`](06-observability-control.md)). Claude Code's interactive approval would be redundant inside the sandbox; Covey's guard rails sit one level above.

As **defence in depth**, `--allowedTools` (and `--permission-mode`) is nevertheless set, to trim the tool scope in the subprocess as well — the soft inner limit in addition to the hard outer one. The standard scope (`daemon.DefaultAllowedTools`) covers the productive basics: reading/writing/editing/searching files (`Read`, `Write`, `Edit`, `Glob`, `Grep`, `NotebookEdit`), shell (`Bash`, `BashOutput`, `KillShell`), web (`WebFetch`, `WebSearch`) as well as `Task`/`TodoWrite`. Web access still runs through the egress proxy — the allowlist remains the hard limit.

## Sub-run in the project checkout

A run starts in the **agent home** (`/home/agent`) — that is where `~/.claude`, the wiki working copy and the dependency caches live. A project's source code, by contrast, lands under `~/repos/<project>-<ref>/` ([`13-zammad-integration.md`](13-zammad-integration.md) describes the pattern for target systems; the GitLab plugin's `checkout` action unpacks the archive there). Claude Code, however, looks for project memory (`CLAUDE.md`), `.claude/agents`, skills and commands **relative to the working directory** — from the home an agent sees none of it.

That costs twice over: the agent re-derives the project structure on every heartbeat run (a fresh process, a capped turn budget), and the project's conventions do not affect the result.

`RunSpec` therefore separates **working directory and home**: `WorkDir` sets the subprocess's cwd, `HomeDir` stays `HOME`. The **sub-run** builds on this — a nested run of the same adapter that starts in the checkout and finds the project's harness there in full. The agent kicks it off through the `dev:agent` action; it is executed by the daemon (`SubAgentRunner`, passed to the plugin via context like `Workdir` and the artefact sink).

The division of roles is the core:

| | Outer run | Sub-run |
|---|---|---|
| Working directory | agent home | project checkout |
| Prompt | the compiled agent config (`SOUL.md` …) | the project's harness + a terse assignment frame |
| Target systems | through the action proxy | **none** — no `COVEY_ACTION_PORT` |
| Task | triage, communication, `commit`, merge request, memory | understand, change, build, test |

The sub-run reaches **no target systems**: without the action proxy it gets to neither the ticket system nor mail and cannot check anything in. That keeps the boundary sharp (communication stays with the agent that knows the protocol) and at the same time denies instructions from foreign repo content a path to the brokered credentials. Part of this is that **no subprocess inherits the daemon's `COVEY_*` environment**: it contains `COVEY_WS_URL` and `COVEY_DAEMON_TOKEN` — with those you could open your own WebSocket to the control plane and send `request_credential`, i.e. address the broker directly. The adapter therefore filters those variables out of every run's environment (`daemon.childEnv`); what a run legitimately needs is passed explicitly by the caller. The `git` calls with which the daemon pulls the file list *after* the sub-run run this way too — `git` executes commands that sit in the repository configuration (`core.fsmonitor`, filters), and after the sub-run that configuration is no more trustworthy than the rest of the checkout.

**What the sub-run does have** — and what follows from it: the auto-discovery that makes it useful in the first place loads *executable* configuration from the repository — hooks, MCP server definitions, skills, subagents. Together with `--dangerously-skip-permissions` that means: **repo content is code that runs in the sandbox.** The outer run in the home never did that; with the sub-run it is a deliberate widening of the attack surface. The target systems are cleanly out of reach (see above), but three things remain within reach:

- the **brokered LLM key** (it has to be in the ENV, or the runtime does not run),
- the **network within the egress allowlist**,
- the **agent home**. `HOME` deliberately stays shared — without `~/.claude` and the dependency caches the sub-run would be neither runnable nor incremental. But that means repo-supplied code also reads the wiki working copy (the agent's memory), other checkouts under `~/repos` and everything else in the home. Together with the permitted egress, that is an exfiltration path.

For repositories of your own organisation this is the right trade-off — they are the source of the code the agent builds and executes anyway. For foreign repositories (a fork, an external assignment) it is not: there the sub-run belongs behind an approval requirement or a ban through the guard rail on `dev:agent`, and the egress allowlist drawn tight. A fine-grained switch ("load the harness, but without hooks/MCP") is not currently provided for — the runtime does not know one.

Observability and cost control are preserved, because the sub-run uses the same protocol messages: its stream-json lines flow (marked as a sub-run) as `event` into the same recording, and its `cost` report goes through `AddCost` and the budget check. **The cap is therefore coarser than for the outer run**: Claude Code only delivers `total_cost_usd` with the result event, i.e. only after up to 200 turns of the sub-run. More can now accumulate between two budget checks than before — a sub-run cannot circumvent the budget, but it can overshoot it. It would only get finer-grained with interim reports from the running session (an open point). The guard-rail subject `dev:agent` makes the sub-run centrally forbiddable or subject to approval.

The marker is an additional key `covey_sub_agent` **inside** the line object, not a wrapper around it: recording and timeline read the stream-json format directly, a wrapper would hide `type` and the sub-run would sit in the recording as a JSON lump instead of as a turn with its tool calls — precisely where the actual work happens. It carries the working directory (`dir`) and an **identifier for the run** (`run`) on every line, plus, **only on the first line**, the assignment (`task`, truncated). Only on the first, because otherwise it would go into the recording at its length × the number of lines; as a headline it suffices once.

The identifier is not cosmetic: the action proxy serves concurrently (a goroutine per request), so two simultaneous `dev agent` calls interleave their lines under the same task — within the same checkout single-flight prevents that (see below), across two different checkouts it stays possible and desirable. Whoever recognises runs by their proximity in the stream then merges them into one block or splits one run into fragments as soon as a control-plane event falls in between. Through the identifier every run stays one block — the timeline groups by it ([`06-observability-control.md`](06-observability-control.md)).

**At most one sub-run runs per checkout**: the action proxy serves every request in its own goroutine and the runtime does call tools in parallel — two runs in the same directory would overwrite each other's files and both report the same cumulative state. A second assignment with the same `cwd` is therefore rejected while the first is working; different directories run in parallel.

So that the agent knows afterwards **what** was changed, the checkout creates a git repository with the upstream state as a baseline commit (the archive brings no `.git` with it) and marks it with the tag `covey-baseline`. The sub-run reports the difference **against that commit** back as `changed_files`/`deleted` — exactly the lists the `commit` action expects. It is measured against a commit and not against a `git status` snapshot from before, because the sub-agent may commit locally in the checkout: many projects require that in their `CLAUDE.md`, and after a commit `git status` shows nothing any more — the work would sit finished on disk and the report would be empty. Both are therefore captured together: what has been committed since the baseline and what lies open alongside it in the working directory. The lists are consequently **cumulative**: they describe the entire state against upstream, including the work of an earlier sub-run of the same task — exactly what belongs in the merge request. They are read NUL-separated and without git quoting (`-z`, `core.quotepath=false`); otherwise `prüfung.go` would arrive in the report as `pr\303\274fung.go` and go on that way into the `commit` action. A side effect of the baseline: project scripts that call `git` work.

## CLI (`-p`) vs. the Agent SDK

Alongside the CLI, Anthropic also offers an **Agent SDK** (Python/TypeScript) for embedding as a library; the official recommendation is the SDK for production automation, the CLI for scripts. Since Covey's daemon runs in **Go** and there is no official Go SDK, the pragmatic route is the **subprocess call to `claude -p`**: start a process, stdin/stdout, exit code — like any other CLI. If the daemon part ever arose in Python/TS, the SDK would be the richer alternative (native message objects, tool approval callbacks).

## Notes / open points

- **`--bare`** skips auto-discovery (hooks/skills/MCP/CLAUDE.md) and makes runs deterministic — recommended for scripts, and it will become the default for `-p`. Trade-off: MCP servers then have to be passed explicitly via `--mcp-config`. Sensible for the **outer** run, because reproducibility beats local incidental config. For the **sub-run** it would be the opposite of the purpose: there the auto-discovery of the project harness is exactly what is wanted (see above) — if `--bare` becomes the default, the sub-run has to opt out of it explicitly.
- **Exit codes:** non-zero on error (e.g. `--max-turns` reached), but no stable global code table — check for ≠ 0, do not assume specific codes.
- **Background tasks** started by Claude Code (e.g. a dev server) are terminated after the result with a short grace period; a cap prevents blocking. Rarely relevant for a support agent, worth noting for later coding agents.
- Checked against the Claude Code headless documentation (`code.claude.com/docs/en/headless`, as of July 2026); the flags evolve — check briefly before starting to build.
