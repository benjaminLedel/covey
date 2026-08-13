# Runtime Fallback Recovery Design

## Problem

The configured Claude-to-Codex fallback does not recover the task that discovers
the Claude limit. The current run parks the Claude credential and then completes
the task as failed. A later wake can also stop before fallback selection when a
runtime credential references a secret that has already been deleted: `Pick`
returns `secret not found`, while the fallback path only handles
`runtimes.ErrExhausted`.

The observed production state contains both conditions: Claude reported
`You've hit your weekly limit`, and the Claude runtime still references a
deleted `anthropic_api_key` while its subscription credential is parked.

## Design

### Credential selection

`runtimes.Store.Pick` treats a missing referenced secret as unavailable
capacity, not as a fatal storage error. It continues through the remaining
healthy credentials. If no resolvable credential remains, it returns
`ErrExhausted`; this is the capacity-layer signal that permits the configured
single-hop fallback. Errors other than the canonical missing-secret error still
propagate unchanged.

This also makes secret deletion self-healing at selection time without hiding
database failures or malformed engine configuration.

### Limit recovery

When `task_done` reports a subscription-window or API rate-limit error,
`rejectionCooldown` classifies it as `runtimes.ReasonLimit`; the orchestrator
parks the credential and reopens the same task instead of completing it as a
terminal task failure. It then ends the current waking phase and starts a new
one. This boundary is essential because engine and credential are intentionally
fixed within a phase; retrying inside the existing task loop would start Claude
again. The new phase selects capacity again, sees the primary as unavailable,
and injects the configured fallback's engine/model into the sandbox.

Only provider-capacity limits take this path. Authentication errors, ordinary
runtime failures, turn-limit continuations, escalations, and business failures
retain their existing behavior.

There is no active retry loop: every successfully classified limit parks the
credential that produced it before reopening the task. If the fallback also
hits a limit, both runtimes are exhausted and the ordinary capacity path leaves
the open task waiting until one becomes usable again. If persisting the
cooldown fails, the task remains terminal rather than retrying without a guard.

### Feature integration

The existing `feat/runtime-fallback` commit is applied to current `main` before
the recovery changes. Migration 0050, the runtime fallback API/UI, Codex sandbox
dependency, and effective engine/model injection remain intact.

## Testing

- A runtime selection test proves a deleted credential is skipped and another
  usable credential is selected.
- A runtime selection test proves all missing/parked capacity yields
  `ErrExhausted`, enabling fallback rather than returning `secret not found`.
- An end-to-end orchestrator test proves the same task moves from a limiting
  primary engine to the configured fallback in a fresh waking phase.
- A second end-to-end case proves a generic provider/API 429 takes the same
  recovery path while retaining its shorter cooldown.
- A boundary test proves an ordinary runtime failure remains terminal.
- Existing runtime, orchestrator, HTTP API, integration, and full Go tests must
  remain green.

## Non-goals

- Arbitrarily deep fallback chains.
- Resuming a Claude session inside Codex.
- General retries for all task failures.
- Silently ignoring secret-store errors other than a missing referenced value.
