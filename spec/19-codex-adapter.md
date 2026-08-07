# 19 — Codex adapter (`codex exec`)

The second engine, and the first one from another provider. It matters less for what it adds than for what it *proves*: the seams described in [`18-runtimes-capacity.md`](18-runtimes-capacity.md) either hold at the second engine or they were never seams. Two of them do not hold as originally written, and this document is where that shows.

Status: **planned, not built.** What is stated here as fact is taken from OpenAI's documentation; everything not yet verified against a running binary is marked as such, because an adapter written against a guessed flag is worse than no adapter.

## Basic mechanics

`codex exec` is the headless subcommand — prompt in, result out, exit — the counterpart to `claude -p` ([`12-claude-code-adapter.md`](12-claude-code-adapter.md)). With `--json` stdout becomes a **JSON Lines stream** carrying thread lifecycle events, turn events and item events (agent messages, command executions, tool calls).

That is the same shape the daemon already consumes: one JSON object per line, forwarded as an `event` message to the control plane → session recording ([`06-observability-control.md`](06-observability-control.md)) without a second mechanism.

## Auth: and the first seam that does not hold

Two credential kinds, and they arrive **in different shapes**:

- **API key** — `CODEX_API_KEY`, an environment variable, and explicitly supported *only* in `codex exec`. Exactly the injection point Covey already uses.
- **ChatGPT plan** — the account login lives in a file (`~/.codex/auth.json`). The documentation describes using it in CI as an advanced pattern rather than a supported default.

This breaks an assumption that was invisible while there was one engine: that a brokered LLM credential is always **an environment variable**. For a pool of ChatGPT subscription seats it would be a *file in the agent home* instead, written before the run and removed after it.

The consequence is in [`18-runtimes-capacity.md`](18-runtimes-capacity.md): an engine declares not only *which* secret it needs but *how* it is delivered — environment variable or file at a path. Without that, Codex-on-subscription becomes a special case in the orchestrator, which is precisely what the engine registry exists to prevent.

The same rule as everywhere applies to the file form: it is written for the run and does not survive it. A credential that stays in the home would be a long-lived secret in the sandbox ([`04-identity-secrets.md`](04-identity-secrets.md)).

> **Note.** Redirecting Codex's configuration directory (so that a run does not read a developer's `~/.codex`) is presumably done through an environment variable of its own. **To be verified** against the binary before implementation.

## Cost: and the second seam that does not hold

The `turn.completed` event carries token counts:

```json
"usage": {"input_tokens": 24763, "cached_input_tokens": 24448,
          "output_tokens": 122, "reasoning_output_tokens": 0}
```

**There is no dollar figure.** Claude Code computes one locally and Covey books it unchanged ([`06-observability-control.md`](06-observability-control.md)); Codex reports consumption and leaves the pricing to the caller.

So the decision "take the number the engine reports" holds only for engines that report one. Covey therefore needs a **price list** — per model, per token kind — and the engine plugin either delivers a cost or delivers tokens the platform prices itself. This is not the fixed-cost apparatus that was deliberately rejected in [`07-open-decisions.md`](07-open-decisions.md) (D13): it is one lookup table, and it produces exactly the same kind of figure Claude Code already produces — a list-price equivalent.

The token kinds also do not map one to one. Covey stores fresh input, cache read and cache write; Codex reports input, **cached input**, output and **reasoning output**. Reasoning tokens are billed as output and belong there; a cache-write counterpart is missing. Translating this is the adapter's job, not the schema's — but it has to be decided rather than approximated, because the three kinds are priced differently and that was the whole point of splitting them.

## Utilisation: not available

Codex usage runs against 5-hour and weekly windows, like a Claude subscription. Unlike Claude Code, the numbers cannot be read from the CLI: `/status` shows a percentage that the community reports as frequently stale, without the window breakdown, and it is an interactive command — unreachable from `codex exec` in any case. The authoritative figures live in the web interface. The issue requesting them in the CLI is open and, at the time of writing, unanswered.

For Codex the source hierarchy from [`18-runtimes-capacity.md`](18-runtimes-capacity.md) therefore falls straight through to its last step: **the platform's own estimate is the only source.** Consumption is measured from what Covey booked against that credential in the rolling window.

That is worth stating plainly rather than treating as a defect: the estimate was designed as a fallback, and at the second engine it is the primary path. An engine capability that only one engine implements is not a capability, it is a special case — the fallback is what makes it a capability.

## Open points before an implementation

Everything below is unverified and has to be established against the binary, not inferred:

- **Session continuation.** Covey's `blocked` state rests on resuming a session ([`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md), and `--resume` in [`12-claude-code-adapter.md`](12-claude-code-adapter.md)). Whether `codex exec` can resume a thread, and under what identifier, decides whether Codex can carry a blocking agent at all or only a stateless one. **This is the most important open question** — an engine that cannot resume covers a smaller part of the agent model. Whatever the answer, it is a **declared capability** ([`18-runtimes-capacity.md`](18-runtimes-capacity.md)): an agent that blocks is refused on an engine without resume when it is assigned, rather than failing when the first answer comes back.
- **Where materialised files go.** Covey writes an agent's skills into its home before every run, today at Claude Code's path (`~/.claude/skills/`). Codex's convention — if it has one — has to be established and declared, otherwise skills are configured, visible and without effect.
- **Turn limit.** Does an equivalent to `--max-turns` exist, and does the truncated case remain distinguishable so that the run can be reported as `incomplete` and continued?
- **Tool scope.** Is there a counterpart to `--allowedTools` for defence in depth? The hard limits sit outside the runtime either way ([`06-observability-control.md`](06-observability-control.md)); this is the inner, soft layer.
- **Working directory vs. home.** The sub-run in the project checkout ([`12-claude-code-adapter.md`](12-claude-code-adapter.md)) requires cwd and `HOME` to be settable separately.
- **Model selection**, and whether it belongs to the runtime (as [`18-runtimes-capacity.md`](18-runtimes-capacity.md) provides) or has to be passed per run.
- **Non-interactive prompts.** Which of the interactive safety questions survive in `codex exec`, and whether a mode comparable to `--dangerously-skip-permissions` is needed and defensible in an isolated sandbox.

## What this engine is *not*

Running Codex against an API key instead of a ChatGPT plan is **not** a second engine — it is the same engine with a different credential kind, and that is exactly what the credential declaration in [`18-runtimes-capacity.md`](18-runtimes-capacity.md) expresses. Whoever creates a second engine for it will maintain two adapters for one binary.

Talking to the provider's API *directly*, without the Codex binary, is a different matter and is treated in [`18-runtimes-capacity.md`](18-runtimes-capacity.md) under "Driving an API directly".

---

**Related:** [`18-runtimes-capacity.md`](18-runtimes-capacity.md) (engines, credentials, capacity) · [`12-claude-code-adapter.md`](12-claude-code-adapter.md) (the first adapter, as a reference) · [`01-architecture.md`](01-architecture.md) (runtime abstraction, daemon protocol)
