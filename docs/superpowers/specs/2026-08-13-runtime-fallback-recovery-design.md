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

When `task_done` reports an error classified by `rejectionCooldown` as
`runtimes.ReasonLimit`, the orchestrator parks the credential and keeps the work
retryable instead of completing it as a terminal task failure. It creates one
child task carrying the same title, body, priority, and a dedicated
`runtime-fallback` origin, then completes the failed attempt with an explicit
reference to that retry. The next wake selects capacity again; because the
primary credential is now parked or unavailable, the configured fallback is
eligible and its engine/model are injected into the sandbox.

Only provider-capacity limits take this path. Authentication errors, ordinary
runtime failures, turn-limit continuations, escalations, and business failures
retain their existing behavior.

To prevent loops, a task whose origin already starts with `runtime-fallback:`
is not automatically retried again. A failure on the fallback is terminal and
visible.

### Feature integration

The existing `feat/runtime-fallback` commit is applied to current `main` before
the recovery changes. Migration 0050, the runtime fallback API/UI, Codex sandbox
dependency, and effective engine/model injection remain intact.

## Testing

- A runtime selection test proves a deleted credential is skipped and another
  usable credential is selected.
- A runtime selection test proves all missing/parked capacity yields
  `ErrExhausted`, enabling fallback rather than returning `secret not found`.
- Orchestrator tests prove a provider limit creates exactly one retry task and
  that a retry-origin task is not retried recursively.
- Existing runtime, orchestrator, HTTP API, integration, and full Go tests must
  remain green.

## Non-goals

- Arbitrarily deep fallback chains.
- Resuming a Claude session inside Codex.
- General retries for all task failures.
- Silently ignoring secret-store errors other than a missing referenced value.
