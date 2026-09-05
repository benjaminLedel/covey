# 25 — SevenCode adapter (a second harness on the gateway)

SevenCode is a coding-agent CLI — a fork of [OpenCode](https://opencode.ai) maintained by Digital Learning GmbH, shipped as one bundled Node programme, and aimed at the educa AI API. `sevencode -p "…"` runs one task headless and prints the answer.

For covey it is the **fourth engine**, and the interesting part is not that there is a fourth. It is that this one sits in front of the **same gateway** [`educa-ai`](23-educa-adapter.md) already drives: educa-ai reaches educa through the Claude Code harness, SevenCode reaches it through a harness of its own. An organisation on educa therefore gets a choice of agent loop — different tool set, different session handling, different cost of a turn — rather than a second choice of endpoint, which [`18-runtimes-capacity.md`](18-runtimes-capacity.md) deliberately does not count as an engine.

Status: **not verified against a binary.** The declaration is read from the CLI's own help output (`sevencode --help`, version 1.0.7), not guessed; the run through covey has not been done, and the parts that need knowledge the help does not give — the event schema, the session id, a system-prompt flag — are absent rather than invented. That is the rule [`19-codex-adapter.md`](19-codex-adapter.md) set, and it costs what it always costs: this engine carries fewer agents than it will once it is verified. What is missing, and the one command that establishes each item, is at the bottom.

## Why this is an engine at all

`18-runtimes-capacity.md` says a different provider behind the same engine is a matter of endpoint and credential, not a new adapter. That sentence holds, and it is not the case here either: what differs is the **harness**, and the harness is what an engine descriptor declares.

SevenCode is not Claude Code with another URL. It has its own permission model (`--plan`, `--accept-edits`, `--auto`, `--yolo` rather than one skip-permissions flag), its own session storage (`~/.sevencode/projects/`), its own credential pair (`SEVENCODE_API_BASE`, `SEVENCODE_API_KEY` rather than an auth token), and it needs Node ≥ 22 in the sandbox where Claude Code needs none. None of that is expressible as "the same engine with another base URL" as long as an engine's declaration is what the control plane validates against — the same argument that made educa an engine, one layer further in.

## Auth

The CLI names two environment variables for CI and containers — the endpoint and the token — and a login command for a person. covey declares both shapes, because they are two kinds of contract and the kind decides the honest unit of a limit ([`18-runtimes-capacity.md`](18-runtimes-capacity.md)):

| Kind | Secret key | Delivery |
|---|---|---|
| metered (`api_key`) | `sevencode_api_token` | as `SEVENCODE_API_KEY` |
| quota (`subscription`) | `sevencode_credentials_json` | as the file `.sevencode/credentials.json`, written before the run, removed after it |

The file is the delivery form `19-codex-adapter.md` introduced: a `sevencode login` artefact is not a variable, and a login left standing in the agent home would be a long-lived secret in the data plane — which [`04-identity-secrets.md`](04-identity-secrets.md) exists to prevent.

The **endpoint is not a secret** and is not delivered as one. `COVEY_SEVENCODE_BASE_URL` on the daemon installation becomes `SEVENCODE_API_BASE` in the run. There is deliberately no default in the adapter: the CLI's own default is a build-time constant nobody here has checked, and a wrong default sends a brokered token somewhere nobody authorised. educa-ai can name its hosted instance because that instance is documented; here the operator names the endpoint or the CLI decides.

The broker path is otherwise unchanged: the control plane picks the credential per waking phase ([`18`](18-runtimes-capacity.md)), hands it over with a TTL, and the daemon puts it into the child environment — last assignment wins, so the endpoint appended by this adapter cannot bury the token that was brokered for the run.

## What the run looks like

`sevencode -p <prompt> --auto`, in the work dir, with `HOME` at the agent home and the daemon's own `COVEY_*` variables stripped (`childEnv`, as with every engine).

- **`--auto`, not `--yolo`.** A headless run has nobody to answer a permission prompt, so the run must not ask. `--auto` is the documented mode that runs through without asking while keeping the CLI's check on every call; `--yolo` drops that check. The hard boundary stays outside the runtime either way — broker, egress, guard-rails ([`06-observability-control.md`](06-observability-control.md)).
- **No `--no-config`.** A project's own `AGENTS.md` belongs to the work, as it does on the other engines.
- **The compiled config travels inside the prompt.** The CLI documents no system-prompt flag, so `SOUL.md` plus the memory context go in front of the task text, in the same message, separated from it. That is a weaker position than a system turn, and it is the position the protocol's closing `COVEY_STATUS:` line is asked from ( [`12`](12-claude-code-adapter.md)).
- **The result is stdout.** `applyStatus` turns the status line into `done` / `blocked` / `escalated` exactly as elsewhere; without a marker the run is `done` with the whole text.

Two flags the other engines pass are absent because nothing documents them: **no turn limit** (`MaxTurns` has no effect, so the handover-on-turn-limit that Claude Code performs — `incomplete` plus a summary turn — does not exist here; the wall-clock timeout still does) and **no tool scope** (`AllowedTools` cannot be narrowed, so the run carries the CLI's built-in set, which is also the prompt-cost argument `12-claude-code-adapter.md` makes for `--tools`). The action proxy's MCP config is not passed either: the target actions then go by shell through the action port, which is the route that existed before MCP and still works.

## What this engine cannot do today

**It cannot resume.** `--resume [id]` is documented; what is not documented is how a run reports the id of the session it just had. A resume flag without an id is a flag that cannot be used, so `Capabilities.Resume` is `false` and `Run()` refuses a resume request plainly instead of starting a fresh run that silently lost the conversation. The consequence is real and lands in the assignment: an agent whose tasks block on a correlated event cannot be assigned to this engine ([`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)). This is not a claim that SevenCode cannot resume — it keeps sessions per project, which suggests it can. It is the state of our knowledge.

**It measures nothing.** `--json` exists, its field names do not, and a guessed token count is not a measurement — the number feeds the capacity limits. So a run on this engine books no tokens, no cache figures and no amount, `RunResult.Measured()` is false and the daemon sends no cost message. The seat shows the run as **unmeasured**, not as free ([`17-kpis.md`](17-kpis.md)). The price list is empty for the same reason educa's is: what a token costs on an instance follows from a contract, not a published table.

**It loads no skills.** `Capabilities.SkillsDir` is empty, so nothing is materialised — rather than written to a directory that may or may not be read. Configured, visible and without effect is the worst of the available failures.

## Open points, and the command that closes each

The adapter is deliberately incomplete in four places. Each line says what to look at and what changes in the code once it is known.

1. **The event schema of `--json`.** `sevencode -p "…" --json` into a file, twice, and read the lines. Then: pass `--json`, forward the lines 1:1 as `runtime` events (the recording gets a transcript again), and map usage into `RunResult`. This is the item that turns the run from "worked" into "observed".
2. **The session id.** Most likely inside those same events; otherwise in `--sessions`, or in the file name under `~/.sevencode/projects/<cwd>/` (each session is stored as `<timestamp>-<id>.jsonl`). Then: capture it into `RunResult.SessionID`, pass `--resume <id>`, flip `Resume`, and the blocking restriction disappears on its own.
3. **A system-prompt flag.** `--help` is short; the binary may know more. If an append-system-prompt equivalent exists, move the compiled config out of the user message — this is what decides how reliably the status line arrives.
4. **Turn limit, tool scope, MCP.** Whether the CLI takes a turn ceiling, a tool whitelist and an MCP server document, and under which flags. If it does, `MaxTurns`, `AllowedTools` and `MCPConfig` become live on this engine rather than documented as inert.

Verification beyond the four: one real multi-step task (read code, find the defect, fix it, re-run the check) before this engine is offered anywhere, as with educa (`runtime_educa_task_test.go`). A run that completes is not a run that works.

## Not done here

Installing the CLI into the sandbox image is left to the image. The base image is `node:26-slim`, so the Node ≥ 22 requirement is met, but the CLI is not in the image and a run without it fails with "is the CLI in the sandbox image?" — the setup steps of the descriptor say how to add it. Pinning that install by digest, the way the marketplace and the workplace catalogue pin their artefacts ([`22`](22-plugin-marketplace.md), [`16`](16-runner.md)), is the shape a third catalogue should take when this engine is offered to a fleet.

---

**Related:** [`23-educa-adapter.md`](23-educa-adapter.md) (the gateway this harness reaches, and why its price is not inherited) · [`12-claude-code-adapter.md`](12-claude-code-adapter.md) (the harness this one competes with, and the seams it defines) · [`19-codex-adapter.md`](19-codex-adapter.md) (the precedent: declare what is verified, leave the rest out) · [`18-runtimes-capacity.md`](18-runtimes-capacity.md) (engines, credentials, what an unmeasured run means) · [`04-identity-secrets.md`](04-identity-secrets.md) (brokered credentials, file delivery)
