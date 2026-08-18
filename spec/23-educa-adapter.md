# 23 — educa AI adapter (gateway engine)

The third engine, and the first that does not sit in front of a *provider* but in front of a **gateway**. educa AI Core puts one bearer token before several LLM backends and serves them in two dialects at once: `/v1/chat/completions` in OpenAI's and `/v1/messages` in Anthropic's — the latter with `tools`, `system`, SSE streaming and `/v1/messages/count_tokens`.

That second dialect is what makes the engine cheap to build honestly. An API is not a harness: an agent that edits files, runs a shell, resumes a session and closes with a `COVEY_STATUS` line needs one, and Covey already has a verified harness that speaks exactly that dialect. So `educa-ai` is **Claude Code with its base URL pointed at educa** ([`12-claude-code-adapter.md`](12-claude-code-adapter.md)), not a second agent loop.

Status: **verified against the hosted instance.** The harness completes a run, executes tools and resumes a session across a second run; `--effort` passes through. The measurements are in `internal/daemon/runtime_educa_live_test.go`, which skips without a token. One defect was measured and is named below rather than worked around.

## Why this is an engine at all

[`18-runtimes-capacity.md`](18-runtimes-capacity.md) says a different provider behind the same engine is *not* a new engine — Claude Code speaks to Bedrock, Vertex and Foundry, and that is a matter of endpoint and credential rather than of a new adapter. The sentence still holds; this case sits just outside it, and the reason is worth naming rather than glossing over.

What differs at educa is not only the endpoint. It is a set of facts a **descriptor declares** and a runtime row cannot:

- **which secrets** the token is looked up under, and in which order two unlike contracts are drawn on;
- **that there is no utilisation source** here, so the platform must fall through to its own estimate;
- **that the harness's dollar figure must not be booked**, because it prices somebody else's contract;
- **that a model is mandatory**, because the harness's default names a model the gateway need not know;
- **which effort levels exist**, since educa documents its ceiling at `xhigh`.

None of those is expressible as "the same engine with another URL" as long as an engine's declaration is what the control plane validates against. So this is an engine. A **second** gateway would be the moment to generalise it — one parameterised gateway engine rather than a third near-copy.

## Auth

One opaque bearer token (`Authorization: Bearer …`), delivered to the run as `ANTHROPIC_AUTH_TOKEN` — the variable the harness uses for a non-Anthropic endpoint.

Two credential kinds are declared for the same value, and the difference is not cosmetic. The kind decides the honest unit of a limit — money where money is spent, the window quota where it is not — and it is what the merit order sorts on ([`18-runtimes-capacity.md`](18-runtimes-capacity.md)): a flat-rate seat is filled before anything metered is touched.

| kind | secret | contract |
|---|---|---|
| `api_key` | `educa_api_token` | billed per token |
| `subscription` | `educa_seat_token` | flat-rate contract seat |

The adapter additionally **clears** `ANTHROPIC_API_KEY` and `CLAUDE_CODE_OAUTH_TOKEN` for the run. That is not tidiness: an Anthropic credential left in the sandbox environment would otherwise be sent to a third party's endpoint, and nothing downstream would notice ([`04-identity-secrets.md`](04-identity-secrets.md)).

## Cost: the figure is not inherited

Claude Code computes a dollar amount itself and Covey books it unchanged. Through a gateway that number prices the **wrong contract**, and for a model the harness's table does not know it becomes zero — the failure the price list was built to avoid: a run priced at zero looks free, and nobody checks it.

The adapter therefore discards the harness's amount and re-derives it from this engine's own price list, which is deliberately **empty**: what educa costs follows from a contract, not from a published table. Token counts stay — they are measured, not inferred — so a run carries its consumption and no amount. The empty list is the seam: whoever knows their rates enters them there and every recorded token count is priced at once.

## Utilisation: not available

`/usage` is a harness command that asks **Anthropic's** account endpoint; behind a gateway it answers about the wrong account or not at all. So `educa-ai` must not declare the capability — and the implementation makes that structural rather than a matter of discipline: it *holds* its `ClaudeCode` in a field instead of embedding it, because embedding would promote `Usage()` and silently make the engine a `UsageReporter`.

educa does report per-token windows under `GET /stats/{token_id}`, but that needs a second key (`STATS_API_KEY`) and the token's id — neither of which the sandbox has, and the stats key is not a credential to broker into one. As with Codex, the source hierarchy falls through to the platform's own estimate.

## The model is mandatory

Which ids exist is the instance's business (`GET /v1/models`, bearer-authenticated). The harness's own default names an Anthropic model the gateway need not route, so a run without a configured model is **refused before the harness starts**, with the curl that lists the ids in the error text. Fail-closed, and the check can be satisfied — which is the whole requirement Covey puts on a check ([`README.md`](../README.md)).

## Egress

The sandbox reaches a model only through the allowlist, and the org default is seeded with `api.anthropic.com` ([`06-observability-control.md`](06-observability-control.md)). An educa agent needs **its own host** there (`api.educaai.de`, or the on-premise one) — this is the first thing a first setup runs into, so it is a setup step in the descriptor rather than a footnote.

The adapter sets `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` so the harness's side traffic (telemetry, error reporting, autoupdate) does not go to the provider. That keeps the allowlist honest: an educa agent then needs the educa host and nothing else. An engine version that does not know the variable ignores it — unlike an invented CLI flag, an unread environment variable costs nothing.

## The on-premise instance

The default endpoint is the hosted one. Another instance is reached with `COVEY_EDUCA_BASE_URL` in the sandbox's environment — the same override idiom the other engines use for their binary. Per-organisation endpoints would need the endpoint to become a runtime field; that is the generalisation named above, and it is not worth a migration for one gateway.

## What was measured

Against `https://api.educaai.de`, engine `educa-ai`, models `gemma-4-26B-A4B-it` and `gpt-oss-120b`:

| | result |
|---|---|
| the harness runs | **yes** — `status=done`, the `COVEY_STATUS` line parsed, a session id returned |
| tools are executed | **yes** — the agent read a file through the tool and reported its contents |
| `--resume` carries the session | **yes** — same session id, the context of the first run present in the second |
| `--effort` | passes through, the run is unaffected |
| the dollar figure | correctly **not** inherited: `cost=0`, the tokens stay |
| a rejected token | produces this engine's own wording, not Anthropic's advice |

Two properties of the instance are worth knowing before an agent is assigned:

**It serves open-weight models over vLLM**, not Claude models — at the time of writing `gemma-4-26B-A4B-it`, `gemma-4-E4B-it`, `gpt-oss-120b`, `EuroLLM-9B-Instruct` and an embedding model. The harness logs `unrecognized_model` and runs anyway. `Qwen-AgentWorld-35B-A3B` is listed by `/v1/models` but answers `500` — a listed model is not a running one, which is the reason the setup step points at the instance rather than at a table in this document.

**`gpt-oss-120b` reports `stop_reason: "end_turn"` on a response that contains a `tool_use` block**, where the Anthropic contract says `"tool_use"`; `gemma-4-26B` gets it right. It does not break the harness — which evidently acts on the presence of the block — but anything else reading `stop_reason` would be misled.

## The measured defect: the input side is lost while streaming

The same request answers

- non-streaming: `usage: {"input_tokens": 61, "output_tokens": 40}`
- streaming: `message_start` with `"input_tokens": 0`, and a `message_delta` carrying `{"output_tokens": 40}` and **no input field at all**.

The harness always streams, so this is the normal case rather than an edge one: a run books its output tokens and reports nothing for the input. Since a prompt is the larger half of almost every agent run, the platform sees a fraction of what its agents read.

It is **not** worked around here. A guessed input count would be indistinguishable from a measured one, and the estimate that feeds the capacity limits ([`18-runtimes-capacity.md`](18-runtimes-capacity.md)) would then run on a number nobody can check. It also settles the price list: entering rates today would apply them to half the tokens, which is not a cheap run but a mismeasured one.

The fix belongs to the endpoint — `message_start` should carry the prompt's `input_tokens`, as the non-streaming path already does.

## Open points

- **Does the thinking budget reach the backend?** `/v1/messages` accepts a `thinking` field without complaint, and `gpt-oss-120b` returns `thinking` content — but it does so without the field as well, so the effect has not been isolated. The levels stay declared: the flag is the harness's, it passes through, and the worst case is a control without effect on a backend that has none.
- **Prompt caching.** Neither cache reads nor cache writes appear in the gateway's usage. Whether vLLM's prefix caching happens behind it and simply is not reported, or does not happen, decides how expensive a long agent session is here.
- **Per-organisation endpoints.** Today the endpoint is one variable for the whole installation; an org running its own educa alongside the hosted one would need it on the runtime row.

---

**Related:** [`18-runtimes-capacity.md`](18-runtimes-capacity.md) (engines, credentials, capacity) · [`12-claude-code-adapter.md`](12-claude-code-adapter.md) (the harness this engine drives) · [`19-codex-adapter.md`](19-codex-adapter.md) (the second engine, and the seams it broke) · [`04-identity-secrets.md`](04-identity-secrets.md) (brokered credentials)
