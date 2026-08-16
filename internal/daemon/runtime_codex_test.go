package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCodexAdapterAllowsNonGitAgentHomes catches the fleet failure where
// `codex exec` rejected a perfectly valid Covey agent home before doing any
// work. Covey's container is the isolation boundary; an agent is not required
// to start in a Git checkout.
func TestCodexAdapterAllowsNonGitAgentHomes(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "codex")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$HOME/args.txt\"\n" +
		"printf '%s\\n' '{\"type\":\"turn.completed\",\"text\":\"done\"}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := (&Codex{Binary: bin}).Run(context.Background(), RunSpec{
		Body: "finish the task", HomeDir: home, WorkDir: home,
		Env: []string{"HOME=" + home},
	}, func(string, json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "done" || res.Result != "done" {
		t.Fatalf("run did not complete: %+v", res)
	}
	args, err := os.ReadFile(filepath.Join(home, "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "--skip-git-repo-check") {
		t.Fatalf("Codex must accept non-Git agent homes, args were:\n%s", args)
	}
}

// Covey already runs Codex inside its per-agent Docker sandbox. Leaving the
// CLI's inner approval/sandbox layer active in a headless run cancels MCP tool
// calls because no user is present to approve them.
func TestCodexAdapterUsesOuterSandboxForHeadlessTools(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "codex")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$HOME/args.txt\"\n" +
		"printf '%s\\n' '{\"type\":\"turn.completed\",\"text\":\"done\"}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := (&Codex{Binary: bin}).Run(context.Background(), RunSpec{
		Body: "use a tool", HomeDir: home, WorkDir: home,
		Env: []string{"HOME=" + home},
	}, func(string, json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(home, "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("headless Codex must rely on Covey's outer sandbox, args were:\n%s", raw)
	}
}

func TestCodexAdapterPassesOpenAIAPIKey(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "codex")
	script := "#!/bin/sh\nprintf '%s' \"$OPENAI_API_KEY\" > \"$HOME/key.txt\"\n" +
		"printf '%s\\n' '{\"type\":\"turn.completed\",\"text\":\"done\"}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	credential, _ := Describe("codex")
	api, _ := credential.Credential(CredAPIKey)
	_, err := (&Codex{Binary: bin}).Run(context.Background(), RunSpec{
		Body: "finish", HomeDir: home, WorkDir: home,
		Env: []string{"HOME=" + home, api.EnvVar + "=test-key"},
	}, func(string, json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(home, "key.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "test-key" {
		t.Fatalf("Codex did not receive OPENAI_API_KEY, got %q", got)
	}
}

func TestCodexAdapterPassesCoveyMCPServer(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "codex")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$HOME/args.txt\"\n" +
		"printf '%s\\n' '{\"type\":\"turn.completed\",\"text\":\"done\"}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := (&Codex{Binary: bin}).Run(context.Background(), RunSpec{
		Body: "finish", HomeDir: home, WorkDir: home,
		Env:       []string{"HOME=" + home},
		MCPConfig: `{"mcpServers":{"covey":{"type":"http","url":"http://127.0.0.1:4567/mcp"}}}`,
	}, func(string, json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(home, "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(raw)), "\n")
	want := `mcp_servers.covey.url="http://127.0.0.1:4567/mcp"`
	found := false
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-c" && args[i+1] == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Codex did not receive the Covey MCP server %q, args were:\n%s", want, raw)
	}
}

func TestCodexAdapterRejectsMalformedMCPConfig(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "codex")
	script := "#!/bin/sh\ntouch \"$HOME/started\"\n" +
		"printf '%s\\n' '{\"type\":\"turn.completed\",\"text\":\"done\"}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := (&Codex{Binary: bin}).Run(context.Background(), RunSpec{
		Body: "finish", HomeDir: home, WorkDir: home,
		Env: []string{"HOME=" + home}, MCPConfig: `{broken`,
	}, func(string, json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "failed" || !strings.Contains(res.Error, "MCP") {
		t.Fatalf("malformed MCP config must fail clearly, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(home, "started")); !os.IsNotExist(err) {
		t.Fatalf("Codex started despite malformed MCP config, stat error: %v", err)
	}
}

func TestCodexAdapterRejectsExternalMCPServer(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "codex")
	script := "#!/bin/sh\ntouch \"$HOME/started\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := (&Codex{Binary: bin}).Run(context.Background(), RunSpec{
		Body: "finish", HomeDir: home, WorkDir: home,
		Env:       []string{"HOME=" + home},
		MCPConfig: `{"mcpServers":{"covey":{"type":"http","url":"https://example.com/mcp"}}}`,
	}, func(string, json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "failed" || !strings.Contains(res.Error, "task-local") {
		t.Fatalf("external MCP server must be rejected clearly, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(home, "started")); !os.IsNotExist(err) {
		t.Fatalf("Codex started with an external MCP server, stat error: %v", err)
	}
}
