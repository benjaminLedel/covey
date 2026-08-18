# 23 — educa AI adapter (gateway engine)

The third engine, and the first that does not sit in front of a *provider* but in front of a **gateway**. educa AI Core puts one bearer token before several LLM backends and serves them in two dialects at once: `/v1/chat/completions` in OpenAI's and `/v1/messages` in Anthropic's — the latter with `tools`, `system`, SSE streaming and `/v1/messages/count_tokens`.

That second dialect is what makes the engine cheap to build honestly. An API is not a harness: an agent that edits files, runs a shell, resumes a session and closes with a `COVEY_STATUS` line needs one, and Covey already has a verified harness that speaks exactly that dialect. So `educa-ai` is **Claude Code with its base URL pointed at educa** ([`12-claude-code-adapter.md`](12-claude-code-adapter.md)), not a second agent loop.

Status: **declared, run unverified against a live instance.** What is written here as fact comes from the instance's own OpenAPI document (`GET /spec`); what has not been measured is marked as such.

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

## Open points, to be measured against a live instance

Unverified, and not to be inferred:

- **Does the harness run against `/v1/messages` end to end** — streaming, `tools`, tool results, the system prompt — or does it use a header or field the gateway drops?
- **Does `usage` come back** on the gateway's responses? Without it the run has no token counts and the platform's estimate has nothing to estimate from.
- **Is `--resume` unaffected?** The session lives in the agent home and is replayed by the harness, so it should be; it is declared `true` on that reasoning and has to be confirmed.
- **Does the thinking budget survive** the pass-through, i.e. does `--effort` reach the backend? The request schema forwards unknown fields, which makes it likely and not certain.
- **Which model ids** the instance serves, and whether any of them is a sensible default worth naming in the setup text.

---

**Related:** [`18-runtimes-capacity.md`](18-runtimes-capacity.md) (engines, credentials, capacity) · [`12-claude-code-adapter.md`](12-claude-code-adapter.md) (the harness this engine drives) · [`19-codex-adapter.md`](19-codex-adapter.md) (the second engine, and the seams it broke) · [`04-identity-secrets.md`](04-identity-secrets.md) (brokered credentials)
