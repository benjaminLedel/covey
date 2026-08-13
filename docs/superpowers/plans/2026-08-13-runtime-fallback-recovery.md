# Runtime Fallback Recovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a Claude provider-limit failure retry the same work once on the configured Codex fallback, even when the primary runtime contains a credential whose secret was deleted.

**Architecture:** The capacity layer converts missing credential references into unavailable capacity while preserving all other errors. The orchestrator converts only `ReasonLimit` task failures into one explicit child retry, so the next wake enters the existing single-hop runtime fallback selector.

**Tech Stack:** Go 1.26, PostgreSQL/pgx, existing Covey runtime and backlog stores, standard `testing` package.

## Global Constraints

- Follow a red-green-refactor cycle for every behavior change.
- Retry only provider capacity limits classified as `runtimes.ReasonLimit`.
- Permit at most one automatic runtime-fallback retry per task chain.
- Preserve existing handling for authentication, ordinary failures, incomplete runs, and escalations.
- Do not expose or log secret values.

---

### Task 1: Integrate the existing runtime fallback feature

**Files:**
- Apply existing commit: `9cd4ffe`
- Verify: `internal/orchestrator/orchestrator.go`, `internal/runtimes/store.go`, `migrations/0050_runtime_fallback.up.sql`

**Interfaces:**
- Consumes: current `main` runtime and orchestrator APIs.
- Produces: `Runtime.FallbackRuntimeID`, `Store.SetFallback`, and `Orchestrator.fallbackCredential`.

- [ ] **Step 1: Apply the existing feature commit**

Run: `git cherry-pick 9cd4ffe`

- [ ] **Step 2: Resolve conflicts without changing the feature semantics**

Run: `git diff --check`

- [ ] **Step 3: Run the existing focused tests**

Run: `go test ./internal/runtimes ./internal/httpapi ./internal/orchestrator`

Expected: PASS.

### Task 2: Treat deleted credential references as unavailable capacity

**Files:**
- Modify: `internal/runtimes/pick.go`
- Test: `internal/runtimes/pick_test.go`

**Interfaces:**
- Consumes: `SecretReader.Value(...)` and the canonical secrets not-found error.
- Produces: selection that continues past missing values and returns `ErrExhausted` when none resolve.

- [ ] **Step 1: Write failing tests**

Add tests with a `SecretReader` that returns the missing-secret error for one
credential. Assert that a second credential is selected; in the all-missing
case assert `errors.Is(err, ErrExhausted)`.

- [ ] **Step 2: Verify the tests fail for the observed reason**

Run: `go test ./internal/runtimes -run 'TestPick.*Missing' -count=1`

Expected: FAIL because `Pick` currently returns `secret not found`.

- [ ] **Step 3: Implement the minimal selection change**

Resolve candidate credentials during selection. Continue only for the canonical
missing-secret error, propagate every other error, and return `Exhausted` if no
candidate resolves.

- [ ] **Step 4: Verify focused tests pass**

Run: `go test ./internal/runtimes -count=1`

Expected: PASS.

### Task 3: Requeue a provider-limit failure exactly once

**Files:**
- Modify: `internal/orchestrator/orchestrator.go`
- Test: `internal/orchestrator/credential_test.go` or a focused new orchestrator test file.

**Interfaces:**
- Consumes: `rejectionCooldown(errorText)`, `Backlog.CreateChild`, and `backlog.Task.Origin`.
- Produces: `runtime-fallback:<parent-task-id>` child origin and an explicit failed-attempt result.

- [ ] **Step 1: Write a failing limit-retry test**

Drive `task_done` with `Status: "failed"` and a weekly-limit error. Assert the
parent is failed with a retry reference and one open child preserves title,
body, and priority.

- [ ] **Step 2: Write a failing loop-guard test**

Use a task whose origin begins `runtime-fallback:` and the same error. Assert no
second child is created.

- [ ] **Step 3: Verify both tests fail correctly**

Run: `go test ./internal/orchestrator -run 'Test.*RuntimeFallback' -count=1`

Expected: FAIL because provider limits are currently terminal task failures.

- [ ] **Step 4: Implement the minimal retry helper**

Classify the error once, park the credential, and create one child before the
normal terminal completion path. Keep non-limit behavior unchanged.

- [ ] **Step 5: Verify focused tests pass**

Run: `go test ./internal/orchestrator -count=1`

Expected: PASS.

### Task 4: Verify the complete feature

**Files:**
- Verify all changed production, migration, API, frontend, and test files.

**Interfaces:**
- Consumes: Tasks 1-3.
- Produces: a merge-ready branch with evidence for the original failure mode.

- [ ] **Step 1: Format and statically check**

Run: `gofmt -w internal/runtimes/pick.go internal/runtimes/pick_test.go internal/orchestrator/orchestrator.go internal/orchestrator/runtime_fallback_test.go`

Run: `go vet ./...`

Expected: exit 0.

- [ ] **Step 2: Run all Go tests**

Run: `go test ./... -count=1`

Expected: PASS.

- [ ] **Step 3: Verify the frontend build**

Run: `npm test -- --run` and `npm run build` from `web/`.

Expected: both commands exit 0.

- [ ] **Step 4: Inspect the final diff**

Run: `git diff --check` and `git status --short`.

Expected: no whitespace errors and only intended files changed.
