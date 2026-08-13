# Codex Action-MCP Design

## Problem

Codex fallback runs authenticate successfully but receive none of Covey's action tools. The daemon only creates an MCP configuration when the global `COVEY_ACTION_MCP` opt-in is enabled, and the Codex adapter ignores `RunSpec.MCPConfig` even when it is present. Consequently, GitLab, browser, development, and Covey actions are unavailable. Agents report the missing tools and still end their turns as `done`, so the fleet appears active while accomplishing no work.

## Scope

This change connects the existing per-task Covey action proxy to the Codex CLI. It does not change target authorization, guardrails, credential brokering, action recording, Claude behavior, or the action schemas.

## Design

The daemon will offer the existing action MCP automatically when the selected runtime is Codex. The existing environment opt-in continues to control MCP for other runtimes, preserving Claude's current behavior.

`RunSpec.MCPConfig` remains the runtime-neutral handoff. The Codex adapter will parse the existing JSON document, extract the streamable HTTP server named `covey`, and pass its URL to `codex exec` as:

```text
-c mcp_servers.covey.url="http://127.0.0.1:<port>/mcp"
```

Only the task-local loopback URL is transferred. No host Codex configuration, external MCP server, header, or secret is inherited.

The Codex CLI runs with its documented bypass for automation that is already externally sandboxed. Without it, a headless MCP call is cancelled because nobody can answer an approval prompt. Covey's per-agent Docker container remains the execution sandbox, and all target actions still pass through the action proxy.

If a non-empty MCP document is malformed, lacks the `covey` server, uses an unsupported transport, or has an invalid URL, the Codex run fails clearly before starting the CLI. It must not silently run without tools.

## Security boundaries

- The MCP server remains bound to the sandbox loopback interface.
- The existing action proxy remains the sole execution path, retaining tool allowlists, target credentials, guardrails, recording, and artifact handling.
- Codex receives no control-plane daemon token and no direct target-system credential.
- Claude and other runtimes retain their existing MCP opt-in behavior.

## Verification

1. A daemon test proves Codex receives an MCP configuration even when the global opt-in is absent.
2. A Codex adapter test uses a fake binary and asserts the exact `mcp_servers.covey.url` configuration argument.
3. A malformed MCP document test proves fail-closed behavior.
4. The full Go test suite and `go vet ./...` pass.
5. After deployment, one isolated agent invokes a real Covey/GitLab action through MCP and completes without a missing-tool response.
6. Only after that canary succeeds are skipped heartbeat signatures reset or heartbeats explicitly fired so the fleet retries the work previously marked `done` without tools.

## Rollback

Reverting the adapter and daemon changes restores the previous shell-only behavior. Runtime credentials and the working `codex_auth_json` subscription login are unaffected.
