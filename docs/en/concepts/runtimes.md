---
slug: runtimes
title: Runtimes and engines
description: 'The engines covey ships — Claude Code, Codex, SevenCode, educa AI and the mock: what each needs, how an agent is put on one, where the CLI comes from and what each cannot do yet.'
faq:
  - q: Which runtimes does covey ship?
    a: 'Five engines are registered in the daemon: Claude Code (claude -p), Codex (codex exec), SevenCode (sevencode -p), educa AI (the Claude Code harness against an educa AI endpoint) and a mock for tests and demos. Claude Code is the one the sandbox images carry and the one the platform is measured against; Codex is declared but its run is not yet verified.'
  - q: Where do I choose the engine of an agent?
    a: 'On the agent page, tab Settings, field Engine — it takes effect at the next task dispatch. The same field is PATCH /api/v1/agents/{id}/runtime. Model and reasoning effort sit beside it; the engine decides which values exist.'
  - q: What is the difference between an engine and a seat?
    a: 'The engine is code: the adapter for one CLI. A seat is configuration: an engine plus the credentials that pay for it and a model. For the simple case a seat appears by itself once a credential is stored under Secrets; it starts to matter with the second credential.'
---

# Runtimes and engines

The **runtime** drives the model loop — it sits between the model and the tool call and decides the next step. covey does not do that itself; it starts a runtime inside the sandbox and speaks to it through a thin adapter in the daemon (`coveyd`). The interface calls such an adapter an **engine**. Every engine is a plugin that registers itself in the daemon, with its setup instructions, the credentials it can run on and what it can and cannot do; the control plane reads the same registry, so there is no second list to keep in step.

**Infrastructure → Engines** shows the engines this build registers, and the ⓘ beside each one opens its setup instructions step by step.

## Engine, seat, agent

Three things, and they are kept apart on purpose:

- The **engine** is code — the adapter for one CLI (`claude-code`, `codex`, `sevencode`, `educa-ai`, `mock`).
- A **seat** is configuration — an engine plus the credentials that pay for it, and optionally a model. Seats are listed under **Infrastructure → Engines → Seats**.
- The **agent** carries the name of its engine and, separately, the seat it works on.

For the simple case there is nothing to configure: store a credential under **Secrets** under the name the engine declares (the table below), create an agent, and the seat appears by itself. The seat matters from the second credential on. Several credentials under one seat are used in a fixed order — a subscription is filled before anything metered is touched, an agent keeps its credential while that one is healthy (the engine caches its prompt per credential), and among equal credentials the least loaded wins. Each credential can carry a limit in a rolling window (dollars for an API key, tokens for a subscription) and can be paused by hand.

An agent is put on an engine on the agent page, tab **Settings**, field **Engine**; the change takes effect at the next task dispatch. **Model** and **Reasoning effort** stand beside it — the engine decides whether it takes any model id, a fixed list, or has no effort control at all, and a field the engine does not have is not shown. Through the API:

```bash
# the engine of an agent
curl -X PATCH https://covey.example.org/api/v1/agents/<id>/runtime \
  -H "Authorization: Bearer covey_…" -d '{"runtime":"codex"}'

# the seats, and moving an agent onto one
curl https://covey.example.org/api/v1/runtime-instances -H "Authorization: Bearer covey_…"
curl -X POST https://covey.example.org/api/v1/agents/<id>/runtime-instance \
  -H "Authorization: Bearer covey_…" -d '{"runtime_id":"<seat id>"}'
```

A seat carries the credentials of its own engine. An agent on one engine that sits on a seat of another would be handed the wrong credential under the wrong variable, so the run refuses it with a message naming both engines, and the seat list marks such an agent (*Sits elsewhere*) with a button to move it.

## The engines at a glance

| Engine | CLI | Credential (secret → how it reaches the run) | Resume | Skills | Effort | Model |
|---|---|---|---|---|---|---|
| `claude-code` | `claude` | `anthropic_api_key` → `ANTHROPIC_API_KEY`, or `claude_code_oauth_token` → `CLAUDE_CODE_OAUTH_TOKEN` | yes | `.claude/skills` | `low` … `max` | any id or alias |
| `codex` | `codex` | `openai_api_key` → `CODEX_API_KEY`, or `codex_auth_json` → the file `.codex/auth.json` | no | none | none | any id |
| `sevencode` | `sevencode` | `sevencode_api_token` → `SEVENCODE_API_KEY` | yes | `.sevencode/skills` | none | the instance's |
| `educa-ai` | `claude` | `educa_api_token` or `educa_seat_token` → `ANTHROPIC_AUTH_TOKEN` | yes | `.claude/skills` | `low` … `xhigh` | fixed list of three |
| `mock` | — | none | yes | `.claude/skills` | as Claude Code | — |

Where two credentials are listed, the first one found wins: an API key stands before a subscription, so whoever holds both spends money on purpose rather than by accident. The value is brokered per waking phase and short-lived; a file credential is written for the run and removed after it. Long-lived secrets do not stay in the sandbox ([Identity and secrets](identity-and-secrets.md)).

**Resume** is what `blocked` rests on: an agent that asks a question and waits for the answer continues the same session when the answer comes ([Backlog and lifecycle](backlog-and-lifecycle.md)). An engine without it can carry agents that finish in one run, not one that waits; the seat list marks it *no resume*.

## Where the CLI comes from

The engine is a CLI inside the sandbox, and it gets there one of three ways:

1. **The workplace image.** Every sandbox image the project publishes carries Claude Code (`npm install -g @anthropic-ai/claude-code` in [`Dockerfile.sandbox`](../../../Dockerfile.sandbox)), which also serves `educa-ai`. No other engine is in the images.
2. **The engine catalogue.** A JSON document behind a URL names releases of an engine, pinned by digest. The runner installs a release on first use into its data directory and mounts it read-only into the sandbox, so no image is rebuilt. The default is the project's catalogue; `COVEY_ENGINE_CATALOG_URL` points at another, on the control plane and on each runner. The project's catalogue carries SevenCode. The full model is in [`spec/26-engine-catalogue.md`](../../../spec/26-engine-catalogue.md).
3. **A path of your own.** Every engine reads a variable that names its binary — `COVEY_CLAUDE_BIN`, `COVEY_CODEX_BIN`, `COVEY_SEVENCODE_BIN` — for an install that stands on the host or in an image of your own ([Workplaces](../operations/workplaces.md)).

When an agent is put on an engine whose CLI is found in none of these places, the assignment answers with a warning rather than letting the first task fail with `executable file not found in $PATH`. It warns, it does not refuse: the CLI can arrive afterwards.

## Claude Code

`claude-code` runs Claude Code headless (`claude -p`, output as `stream-json`), and it is the engine the rest of the platform is measured against: resume through `--resume`, the system prompt through `--append-system-prompt`, skills from `.claude/skills`, the target-system actions as an MCP server, the tool scope through `--allowedTools`, and reasoning effort through `--effort`. It reports its own cost, which covey books unchanged, and on a subscription the provider's own utilisation of the window.

It needs one of two credentials:

- **Subscription** (Pro/Max): `claude setup-token` in a terminal gives a token beginning `sk-ant-oat…`; store it as `claude_code_oauth_token`. It uses the subscription's quota.
- **API key** (billed per token): a key beginning `sk-ant-api…` from the Anthropic console; store it as `anthropic_api_key`.

Both are checked against Anthropic when they are stored, and a value under the wrong key is refused with a note saying which key it belongs under. Without either, a task fails with "Not logged in · Please run /login": the sandbox has its own empty `HOME`, and a login on your own machine is not visible in there. `api.anthropic.com` is part of every organisation's base egress allowlist from the start.

Details: [`spec/12-claude-code-adapter.md`](../../../spec/12-claude-code-adapter.md). `COVEY_RUNTIME_TOOLS` controls which of Claude Code's built-in tools a run has ([Deployment](../operations/deployment.md)).

## Codex

`codex` runs OpenAI Codex headless (`codex exec --json`). **Its status is "declared, run not verified":** the adapter and both credential forms are implemented, but the run has not been checked against the binary, and what OpenAI's documentation does not settle is left out rather than guessed.

- **Credentials:** an API key from the OpenAI platform as `openai_api_key` (reaches the run as `CODEX_API_KEY`), or a ChatGPT plan login (Plus/Pro/Team) — the contents of `~/.codex/auth.json` after `codex login` — as `codex_auth_json`, written into the agent home for the run and removed after it. Neither is checked when it is stored.
- **No resume.** Whether `codex exec` can continue a session is not established, so the engine does not claim it: an agent on Codex should finish in one run and cannot wait for an answer.
- **No skills, no effort, no turn limit, no tool scope.** Skills are not written (Codex's place for them is not established), the reasoning-effort control is not offered, and the turn limit and tool list are not passed.
- **Cost from a price list.** Codex reports tokens and no money; covey prices them per model. Utilisation of a ChatGPT plan cannot be read from the CLI, so a limit rests on what covey booked itself.
- **The CLI is in no image and in no catalogue.** Set `COVEY_CODEX_BIN` to an install, or use an image or a catalogue of your own.

Details and the open points: [`spec/19-codex-adapter.md`](../../../spec/19-codex-adapter.md).

## SevenCode

`sevencode` runs the SevenCode coding-agent CLI headless (`sevencode --json -p …`) against an educa AI endpoint — a second agent loop in front of the same gateway `educa-ai` reaches through Claude Code.

- **The 1.0 line, not the npm package.** Two programs answer to the name. The npm package `sevencode` (`0.0.x`) is a different CLI with a different surface; the adapter drives the 1.0 line. `sevencode --version` says which one is installed.
- **Where the CLI comes from:** the project's engine catalogue, as one Node bundle (Node 22.13 or newer, which the sandbox images carry). The download sits behind a login: store the token as a secret named `COVEY_SEVENCODE_DOWNLOAD_TOKEN` (the agent's own or one assigned to it), or set a variable of that name on the runner host. Assigning the engine to an agent that has no such secret warns about it. Alternatively, point `COVEY_SEVENCODE_BIN` at an install.
- **Credentials:** an API token for the educa AI instance as `sevencode_api_token`; it reaches the run as `SEVENCODE_API_KEY`. The endpoint is `SEVENCODE_API_BASE`, set by the catalogue entry (`https://sevencode.app`) or by the host; covey does not invent one. The endpoint has to be reachable from the sandbox through the agent's egress allowlist.
- **Resume:** yes. The session is read from where the CLI keeps it in the agent home (`SEVENCODE_HOME` points there), and a resumed run whose session is not there is refused before the CLI starts.
- **What it does not have:** a system-prompt flag (the compiled configuration goes in front of the task in the same message), a turn limit, a tool scope, an effort control, a model list. The run starts in the CLI's `--auto` mode; what the agent may reach outside the sandbox is decided by the broker, the egress point and the guard rails, not by the CLI.
- **Cost in tokens.** The CLI reports token counts and no price, so consumption on this engine is booked in tokens.

Known limitations, open at the time of writing:

- The activity feed shows almost nothing of a SevenCode run — lines of the form "runtime event: …" instead of what the agent says and which tools it calls. The recording holds the full event stream ([#319](https://github.com/benjaminLedel/covey/issues/319)).
- A run that ends after one turn without a tool call, without a status line and with an empty result is recorded as `done` ([#299](https://github.com/benjaminLedel/covey/issues/299)).

Details: [`spec/25-sevencode-adapter.md`](../../../spec/25-sevencode-adapter.md).

## educa AI

`educa-ai` drives the same Claude Code binary against educa AI Core's Anthropic-compatible endpoint (`https://api.educaai.de`; `COVEY_EDUCA_BASE_URL` in the sandbox environment points at an instance of your own). Because the harness is Claude Code, resume and skills work as they do there.

- **Credentials:** one bearer token from the instance, stored by what the contract is — `educa_api_token` (billed per token) or `educa_seat_token` (a flat-rate seat). Both reach the run as `ANTHROPIC_AUTH_TOKEN`.
- **Models:** a fixed list of the ids that solved a real multi-step task through the harness — `gemma-4-26B-A4B-it` (the default), `gpt-oss-120b`, `gemma-4-E4B-it`. Other ids the instance lists are refused where they are entered.
- **Effort:** `low` to `xhigh`; Claude Code's `max` is not offered.
- **Egress:** allow the endpoint (`api.educaai.de`, or your own host) in the agent's egress allowlist, or the sandbox reaches no model.

Details: [`spec/23-educa-adapter.md`](../../../spec/23-educa-adapter.md).

## Mock

`mock` runs a scripted answer without a model — no credential, no cost. It is there for demos and for the integration tests, which run the full path through the daemon with it.

## Further reading

- [`spec/18-runtimes-capacity.md`](../../../spec/18-runtimes-capacity.md) — engines, seats, credentials, the merit order and limits
- [`spec/26-engine-catalogue.md`](../../../spec/26-engine-catalogue.md) — where an engine's binary comes from
- [Core concepts](../introduction/core-concepts.md) — runtime, daemon, workplace
