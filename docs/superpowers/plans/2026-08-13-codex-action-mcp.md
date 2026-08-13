# Codex Action-MCP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Codex fallback runs receive and invoke the existing Covey action tools instead of completing tasks without GitLab, browser, development, or Covey capabilities.

**Architecture:** The daemon will force creation of the existing loopback Action-MCP configuration only when the selected runtime is `codex`; other runtimes retain the current environment opt-in. The Codex adapter will validate the runtime-neutral JSON configuration and translate the `covey` streamable-HTTP URL into an invocation-local `mcp_servers.covey.url` CLI override.

**Tech Stack:** Go 1.26, Codex CLI configuration overrides, Covey Action-MCP JSON-RPC server, Docker Compose, PostgreSQL.

## Global Constraints

- Preserve Claude and other runtime behavior.
- Preserve the action proxy as the only tool-execution boundary, including allowlists, guardrails, credential brokering, recording, and artifacts.
- Inject only the task-local `127.0.0.1` Covey MCP URL; inherit no host MCP configuration or secrets.
- Fail clearly before starting Codex when a non-empty MCP configuration is malformed or unsupported.
- Use strict red-green TDD for production changes.

---

### Task 1: Offer Action-MCP automatically to Codex

**Files:**
- Modify: `internal/daemon/actionmcp.go`
- Modify: `internal/daemon/actionmcp_test.go`
- Modify: `internal/daemon/daemon.go`

**Interfaces:**
- Consumes: `cfg.Runtime string` selected by the control plane.
- Produces: `func (p *actionProxy) mcpConfig(force bool) string`; `force=true` bypasses only the environment opt-in, never the empty-tool guard.

- [ ] **Step 1: Write the failing behavior test**

Extend `TestActionMCPConfigIsOptIn` with a real proxy listener and assert that `p.mcpConfig(true)` contains the literal server name `covey` and the proxy's `http://127.0.0.1:<port>/mcp` URL while `p.mcpConfig(false)` remains empty without `COVEY_ACTION_MCP`.

- [ ] **Step 2: Run the focused test and verify RED**

Run: `go test ./internal/daemon -run TestActionMCPConfigIsOptIn -count=1`

Expected: compile failure because `mcpConfig` does not accept the `force` argument.

- [ ] **Step 3: Implement the minimal runtime-specific enablement**

Change the method to:

```go
func (p *actionProxy) mcpConfig(force bool) string {
    if (!force && !actionMCPEnabled()) || len(p.tools) == 0 {
        return ""
    }
    // existing JSON generation
}
```

Pass `cfg.Runtime == "codex"` from `runTask`:

```go
MCPConfig: proxy.mcpConfig(cfg.Runtime == "codex"),
```

- [ ] **Step 4: Run focused daemon tests and verify GREEN**

Run: `go test ./internal/daemon -run 'TestActionMCP|TestCodex' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the daemon-side enablement**

```bash
git add internal/daemon/actionmcp.go internal/daemon/actionmcp_test.go internal/daemon/daemon.go
git commit -m "fix: offer action MCP to Codex runs"
```

### Task 2: Pass the Covey MCP server to Codex

**Files:**
- Modify: `internal/daemon/runtime_codex.go`
- Modify: `internal/daemon/runtime_codex_test.go`

**Interfaces:**
- Consumes: `RunSpec.MCPConfig string` with `mcpServers.covey.type="http"` and a loopback URL.
- Produces: invocation arguments `-c`, `mcp_servers.covey.url="<URL>"`; failed `RunResult` for invalid non-empty configuration.

- [ ] **Step 1: Write the failing CLI-boundary test**

Add `TestCodexAdapterPassesCoveyMCPServer` using a fake executable that writes each argument to `$HOME/args.txt`. Run it with this hand-authored fixture:

```json
{"mcpServers":{"covey":{"type":"http","url":"http://127.0.0.1:4567/mcp"}}}
```

Assert adjacent arguments `-c` and `mcp_servers.covey.url="http://127.0.0.1:4567/mcp"` exist. This test fails if the adapter ignores `MCPConfig`, uses the wrong Codex key, or loses TOML quoting.

- [ ] **Step 2: Write the failing fail-closed test**

Add `TestCodexAdapterRejectsMalformedMCPConfig`. Supply `{broken`, use a fake executable that creates `$HOME/started`, and assert the result is `failed`, its error mentions `MCP`, and `started` does not exist.

- [ ] **Step 3: Run the focused tests and verify RED**

Run: `go test ./internal/daemon -run 'TestCodexAdapter(PassesCoveyMCPServer|RejectsMalformedMCPConfig)' -count=1`

Expected: first test reports missing arguments; second reports that the fake binary started.

- [ ] **Step 4: Implement strict MCP parsing and CLI translation**

Add a private parser that decodes only the required shape, requires `type == "http"`, parses the URL, and requires `scheme == "http"`, `hostname == "127.0.0.1"`, and path `/mcp`. Return:

```go
[]string{"-c", "mcp_servers.covey.url=" + strconv.Quote(server.URL)}
```

In `Codex.Run`, parse before `exec.CommandContext`; on error set `res.Error = "codex MCP config: " + err.Error()` and return the failed result without starting the CLI.

- [ ] **Step 5: Run focused tests and verify GREEN**

Run: `go test ./internal/daemon -run 'TestCodex|TestActionMCP' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the adapter integration**

```bash
git add internal/daemon/runtime_codex.go internal/daemon/runtime_codex_test.go
git commit -m "fix: connect Codex to Covey action tools"
```

### Task 3: Verify, deploy, and prove a real tool call

**Files:**
- Modify if needed: `docs/superpowers/specs/2026-08-13-codex-action-mcp-design.md`
- Modify if needed: PR #57 description

**Interfaces:**
- Consumes: final branch image and existing `codex_auth_json` subscription credential.
- Produces: green CI, a healthy local stack on the final commit, and a recording containing an MCP tool call rather than a missing-tool message.

- [ ] **Step 1: Run formatting and complete verification**

Run:

```bash
gofmt -w internal/daemon/actionmcp.go internal/daemon/actionmcp_test.go internal/daemon/daemon.go internal/daemon/runtime_codex.go internal/daemon/runtime_codex_test.go
go test ./... -count=1
go vet ./...
git diff --check
```

Expected: all commands exit 0.

- [ ] **Step 2: Push the branch and wait for PR checks**

```bash
git push fork feat/runtime-fallback
gh pr checks 57 --repo benjaminLedel/covey --watch
```

Expected: all required checks pass.

- [ ] **Step 3: Build both local images from the final commit**

Build `covey-covey:latest` from `Dockerfile` and `covey-sandbox:latest` from `Dockerfile.sandbox`, embedding the final commit and commit date.

- [ ] **Step 4: Deploy with the fleet temporarily stopped**

Use the authenticated API to kill the fleet, recreate the control-plane container, remove only ephemeral `covey-sandbox-*` containers, and verify `/healthz`, the embedded commit, and `fleet_killed=true`.

- [ ] **Step 5: Run one isolated tool canary**

Leave all agents individually killed, clear only the organization fleet flag, resume `it-arch`, and explicitly fire one heartbeat. Verify its recording contains a Codex MCP tool call to `mcp__covey` and a real action result. A final message claiming tools are unavailable does not pass.

- [ ] **Step 6: Resume and replay skipped work**

After the canary passes, call `/api/v1/fleet/resume`. Reset `last_work_sig` only for heartbeats whose Codex runs after `2026-08-13 08:32:00+00` completed with missing-tool messages, then explicitly fire those heartbeats through the normal API. Do not retry unrelated historical failures.

- [ ] **Step 7: Verify fleet progress and update PR**

Confirm zero kill switches, new `turn.completed` events, new MCP action recordings, and no new auth/trust/missing-tool failures. Update PR #57 with the MCP root cause, tests, and live canary evidence.
