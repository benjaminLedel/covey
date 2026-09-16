# 25 — SevenCode adapter (a second harness on the gateway)

SevenCode is a coding-agent CLI aimed at the educa AI API. `sevencode run "…" --format json` runs one task headless and prints its events.

For covey it is the **fourth engine**, and the interesting part is not that there is a fourth. It is that this one sits in front of the **same gateway** [`educa-ai`](23-educa-adapter.md) already drives: educa-ai reaches educa through the Claude Code harness, SevenCode reaches it through a harness of its own. An organisation on educa therefore gets a choice of agent loop — different tool set, different session handling, different cost of a turn — rather than a second choice of endpoint, which [`18-runtimes-capacity.md`](18-runtimes-capacity.md) deliberately does not count as an engine.

Status: **the surface is measured, one real task is still outstanding.** Flags, events, session handling, credentials and skills below were taken from the shipped binary and from a run against a local double; what is still missing is a multi-step task against the real gateway.

## What this document rests on, and what it got wrong

The binary is the source of truth. The version matters more here than elsewhere, because there are **two countings**: the npm package `sevencode` publishes `0.0.0-…`, `0.0.1`, `0.0.2` — `--version` on the installed CLI answers `0.0.2` (13.09.2026) — while the source project's `package.json` counts `1.0.7`, `1.0.16`. An image can only be pinned against the first, and only `sevencode --version` says what is actually installed.

The first revision of this page described `-p`, `--auto`, `--json`, `--resume` and `SEVENCODE_API_BASE` from a `--help` of version 1.0.7. **That description was of the 1.0 line, and it was right** — what it left unsaid was that the binary installed beside it was the npm build, a different program. The correction written in the next revision ("the shipped CLI does not have those flags") was therefore measured against the wrong artefact and made the page wrong about the engine it documents.

**The name covers two programs, and this page held both of them in one column** — which is not a typo but the reason an adapter drove the wrong CLI for a while. Writing down which is which is the point of this section.

- **The npm package `sevencode`** publishes `0.0.0-…`, `0.0.1`, `0.0.2` and nothing since 31.03.2026, so `--version` answers `0.0.2`. It is an **opencode** build: the help prints `opencode …`, the wrapper honours `OPENCODE_BIN_PATH`, configuration lives under `~/.config/opencode`, every variable it reads is named `OPENCODE_*`, and its artefacts are Bun-compiled platform binaries (`sevencode-core-<os>-<arch>`).
- **The engine covey ships** is the 1.0 line, built from the project's own source: one bundle `build/sevencode.js` (esbuild, `target: node22`, Node ≥ 22.13), so **one artefact serves every platform** and the Node that the sandbox image already carries runs it. Surface: `-p <text>`, `--json`, `--ask`/`--plan`/`--accept-edits`/`--auto`/`--yolo`, `-c`/`--continue`, `--resume`, `--model`, `--no-config`; endpoint in `SEVENCODE_API_BASE`, token in `SEVENCODE_API_KEY`. Home is `~/.sevencode` (override `SEVENCODE_HOME`): `credentials.json`, `skills/<name>/SKILL.md`, `AGENTS.md`, sessions under `projects/<project-slug>/<file>.jsonl`.

| Measured from the 0.0.2 build — true of it, and of nothing else | The 1.0 line |
|---|---|
| `run [message..]`; inside `run`, `-p` is `--password` | `-p <text>` — the prompt is the flag's **value**; a flag it does not know is **ignored**, not rejected |
| `--format json`, on `run` | `--json` — one JSON line per event, the event union written out unchanged |
| `-s`/`--session <id>`, `-c`, `--fork` | `--resume [id]`, `-c`/`--continue` — and an unknown id exits **0 having written nothing** |
| permissions come from the configuration; `run` rejects what it is not allowed | a mode chosen at the command line and decided in advance — nothing brokers a single call, so the caller decides once |
| provider and endpoint are configuration; `{env:VAR}` reads a variable into it | `SEVENCODE_API_BASE` + `SEVENCODE_API_KEY`; `.env` in the work directory is read either way |
| login at `<data>/auth.json` | login at `<home>/credentials.json` |

The 1.0 line also brings two behaviours that a sandbox meets head-on: it **updates itself during a plain `-p` run** (throttled, replacing its own file — `SEVENCODE_NO_AUTO_UPDATE=1` on a read-only engine layer), and it reports **why a turn ended** (`done.stoppedBy`: model, turns, truncated). A turn limit cannot be *set* on this engine, but it can now be *read*, which is the difference between a run that finished and one that ran out.
| sessions in `~/.sevencode/projects/` | a SQLite database under the data directory; `session list`, `export <id>` |
| `SEVENCODE_API_BASE` + `SEVENCODE_API_KEY` | neither; provider and endpoint are configuration, `{env:VAR}` reads a variable into it |
| login file `.sevencode/credentials.json` | `<data>/auth.json`, i.e. `~/.local/share/opencode/auth.json` |

There is a second lesson in that table, and it is uncomfortable. An earlier revision **did** name a public repository whose README described `run`, `--format json` and `--model provider/model` — and that attribution was removed as "a different program with a different surface". It was not a different program. The description was right and the help that displaced it was wrong. So the rule this page opened with holds, with the emphasis moved: read the binary, and when a second source disagrees with what you think you read, run it rather than choosing.

**How the rest was measured.** A run needs a provider, and a provider costs money. So the CLI was pointed at a **local OpenAI-compatible double** (a provider entry with `npm: @ai-sdk/openai-compatible` and a `baseURL` on 127.0.0.1) and asked to do a task. That is what made the event schema, the session handling, the system prompt and the skill discovery measurable without a gateway and without a bill — and it is the cheapest way to check the next version, too.

## Why this is an engine at all

`18-runtimes-capacity.md` says a different provider behind the same engine is a matter of endpoint and credential, not a new adapter. That holds, and it is not the case here: what differs is the **harness**, and the harness is what an engine descriptor declares. A different tool set, a different session store, a different permission model, its own configuration document, Node ≥ 22 in the sandbox — none of that is "the same engine with another base URL", which is the argument that made educa an engine, one layer further in.

## Auth

The CLI reads **no credential variable of its own**. Its provider entry names the key, and `{env:VAR_NAME}` in the configuration reads it out of the environment. So covey brokers the token into a variable of its own naming and the operator's configuration references it:

| Kind | Secret key | Delivery |
|---|---|---|
| metered (`api_key`) | `sevencode_api_token` | as `SEVENCODE_API_KEY`, referenced from the provider entry as `"apiKey": "{env:SEVENCODE_API_KEY}"` |
| token (`api_key`) | `sevencode_api_token` | as the variable `SEVENCODE_API_KEY` (the 1.0 line stores a login itself under `<home>/credentials.json`; covey does not write it, and the 0.0.2 location `~/.local/share/opencode/auth.json` is where the 1.0 line never looks) |

The file is the delivery form [`19-codex-adapter.md`](19-codex-adapter.md) introduced: a login artefact is not a variable, and a login left standing in the agent home would be a long-lived secret in the data plane, which [`04-identity-secrets.md`](04-identity-secrets.md) exists to prevent.

**The endpoint is not covey's to name.** There is no base-URL variable to set, because the CLI reads none: where the model sits is part of the provider entry, and that entry belongs to the operator. covey therefore delivers the token and documents the variable to reference — it invents no endpoint, which would send a brokered token somewhere nobody authorised.

The broker path is otherwise unchanged: the control plane picks the credential per waking phase ([`18`](18-runtimes-capacity.md)), hands it over with a TTL, and the daemon puts it into the child environment — last assignment wins, so nothing this adapter appends can bury the token that was brokered for the run.

## What the run looks like

`sevencode run --format json [--session <id>] [--model …] [--variant …] <message>`, in the work dir, with `HOME` at the agent home and the daemon's own `COVEY_*` variables stripped (`childEnv`, as with every engine).

- **The message is positional and goes last**, so nothing after it can be read as an option.
- **`OPENCODE_PERMISSION` is set by the adapter.** Without it the run may do nothing: a permission request in `run` is answered by the CLI itself with "auto-rejecting", so an agent whose bash tool is refused looks like an agent that cannot work. This is the replacement for the `--auto` the first draft invented. It is not a hole in the guard rails — what an agent may reach outside its sandbox is decided by the broker, the egress point and the policy engine ([`06-observability-control.md`](06-observability-control.md)), all of them outside the runtime.
- **The events are the record.** Every line goes into the recording as it came, exactly as with Claude Code.
- **The result is the text parts.** `applyStatus` turns the closing status line into `done` / `blocked` / `escalated` as elsewhere; without a marker the run is `done` with the whole text.

### The event stream

One JSON line per event, common envelope `{type, timestamp, sessionID, part}`. The emitter in the binary knows exactly these:

| `type` | What it carries |
|---|---|
| `step_start` | the start of a step |
| `text` | `part.text` — emitted only once the part is finished |
| `reasoning` | only with `--thinking` |
| `tool_use` | `part.tool`, `part.state.status` (`completed`/`error`), `part.state.error` |
| `step_finish` | `part.reason`, `part.cost`, `part.tokens{total,input,output,reasoning,cache{read,write}}` |
| `error` | a session error: `error.name`, `error.data.message` |

Two consequences, and both remove a restriction this page used to describe:

**It resumes.** The session id stands on *every* event, and `run --session <id>` continues that session — checked against the double, which received the earlier turns with the second run. `Capabilities.Resume` is therefore `true`, and the restriction that an agent on this engine must not block ([`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)) falls away. A resumed run sends only the new input: the CLI keeps the history, so repeating the brief would be a second one on top of the first.

**It measures.** `step_finish` carries the token figures and a cost, so a run books what it consumed and `RunResult.Measured()` is true. The price list stays empty for the same reason educa's is ([`23`](23-educa-adapter.md)): what a token costs on an instance follows from a contract, not from a published table — and the figure the CLI reports is taken as it comes rather than priced a second time.

**It loads skills.** A skill under `.opencode/skill/<name>/SKILL.md` is found — measured: the description of the CLI's own `skill` tool named the test skill back. `Capabilities.SkillsDir` is `.opencode/skill`.

## Open points

1. **The system prompt.** The CLI has no flag for one, so the compiled config still goes in front of the task in the same message — a weaker position than a system turn, and it is the position the closing `COVEY_STATUS:` line is asked from. There **is** a measured way out: an agent definition under `.opencode/agent/<name>.md` replaces the CLI's own system prompt (checked — the system message the provider received began with the file's text). That whole question was the 0.0.2 build's: it moved one directory holding both the operator's provider entry and covey's files, and covey could not claim it. **The 1.0 line has no such knot** — `SEVENCODE_HOME` relocates the engine's home (covey points it at the agent home, where a session and a login already belong), and instructions are read from `AGENTS.md` there. The system prompt stays in the same message as the task; the way out is now an ordinary file in a directory covey owns, not a decision about ownership.
2. **The reasoning lever.** `--variant` exists and its values are the provider's ("high", "max", "minimal" per the help). `EffortLevels` therefore stays empty — a level that is wrong for the instance's provider is a run-time error, not a choice — and what somebody enters is passed through.
3. **Turn limit, tool scope, MCP.** `MaxTurns` and `AllowedTools` are still inert on this engine, and the action proxy's MCP document is not passed: the target actions go by shell through the action port. The CLI has an `mcp` subcommand and a `--agent` with its own tool list; both are the route to make these live.
4. **One real task.** A multi-step task against the real gateway (read code, find the defect, fix it, re-run the check) before this engine is offered anywhere, as with educa (`runtime_educa_task_test.go`). A run that completes is not a run that works.

## Not done here

Installing the CLI is left to the image or to the **engine catalogue** ([`26-engine-catalogue.md`](26-engine-catalogue.md)), which is the shape this page asked for before that catalogue existed. One property is worth writing down for whoever adds the entry: the npm package carries a `postinstall`, and it does not need it — the wrapper resolves its platform binary (`sevencode-core-<platform>-<arch>`, an optional dependency) by itself at run time, so the catalogue's default `--ignore-scripts` is enough.

Which engine an agent is set to, and whether its CLI can be found at all, is answered before the first run rather than by it ([`26`](26-engine-catalogue.md), #221).

---

**Related:** [`23-educa-adapter.md`](23-educa-adapter.md) (the gateway this harness reaches, and why its price is not inherited) · [`12-claude-code-adapter.md`](12-claude-code-adapter.md) (the harness this one competes with, and the seams it defines) · [`19-codex-adapter.md`](19-codex-adapter.md) (the precedent: declare what is verified, leave the rest out) · [`26-engine-catalogue.md`](26-engine-catalogue.md) (where the binary comes from) · [`18-runtimes-capacity.md`](18-runtimes-capacity.md) (engines, credentials, what an unmeasured run means) · [`04-identity-secrets.md`](04-identity-secrets.md) (brokered credentials, file delivery)
