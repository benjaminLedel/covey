package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeClaude creates a fake `claude` binary that writes its arguments to
// args.txt and emits a predefined stream-json protocol.
func fakeClaude(t *testing.T, script string) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin = filepath.Join(dir, "claude")
	content := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + dir + "/args.txt\"\n" + script + "\n"
	if err := os.WriteFile(bin, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

func TestClaudeCodeAdapterFlagsAndResult(t *testing.T) {
	bin, dir := fakeClaude(t, `
cat <<'EOF'
{"type":"system","subtype":"init","session_id":"sess-123"}
{"type":"assistant","message":{"content":"denke nach"}}
{"type":"result","subtype":"success","session_id":"sess-123","result":"Erledigt.\nCOVEY_STATUS: {\"status\":\"done\",\"result\":\"Ticket beantwortet\",\"memory\":\"Kunde nutzt Chrome\"}","total_cost_usd":0.42,"usage":{"input_tokens":100,"output_tokens":50}}
EOF`)
	adapter := &ClaudeCode{Binary: bin}

	var events []string
	res, err := adapter.Run(context.Background(), RunSpec{
		TaskID: "t1", Title: "Ticket 42", Body: "Bitte prüfen",
		SystemPrompt: "Du bist der Support-Agent.",
		Model:        "claude-opus-4-8",
		AllowedTools: []string{"Bash", "Read"},
		MaxTurns:     30,
		HomeDir:      dir,
	}, func(kind string, payload json.RawMessage) {
		events = append(events, string(payload))
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "done" || res.Result != "Ticket beantwortet" || res.Memory != "Kunde nutzt Chrome" {
		t.Fatalf("result wrong: %+v", res)
	}
	if res.SessionID != "sess-123" {
		t.Fatalf("session_id missing: %+v", res)
	}
	if res.CostUSD != 0.42 || res.InputTokens != 100 {
		t.Fatalf("cost wrong: %+v", res)
	}
	if len(events) != 3 {
		t.Fatalf("every stream-json line must flow as an event, got %d", len(events))
	}

	args, err := os.ReadFile(filepath.Join(dir, "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(args)
	for _, want := range []string{"-p", "Ticket 42", "--output-format", "stream-json",
		"--append-system-prompt", "Du bist der Support-Agent.", "--allowedTools", "Bash,Read",
		"--max-turns", "30", "--model", "claude-opus-4-8", "--dangerously-skip-permissions"} {
		if !strings.Contains(got, want) {
			t.Fatalf("flag %q missing from args:\n%s", want, got)
		}
	}
	if strings.Contains(got, "--resume") {
		t.Fatal("without a resume session --resume must not be set")
	}
}

func TestClaudeCodeAdapterResume(t *testing.T) {
	bin, dir := fakeClaude(t, `
cat <<'EOF'
{"type":"result","subtype":"success","session_id":"sess-123","result":"COVEY_STATUS: {\"status\":\"done\",\"result\":\"fortgesetzt\"}","total_cost_usd":0.1,"usage":{"input_tokens":10,"output_tokens":5}}
EOF`)
	adapter := &ClaudeCode{Binary: bin}
	res, err := adapter.Run(context.Background(), RunSpec{
		TaskID: "t1", Title: "Ticket 42", Body: "ursprünglicher text",
		ResumeSessionID: "sess-123",
		ResumeInput:     "Kunde: es war Chrome",
		HomeDir:         dir,
	}, func(string, json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "done" || res.Result != "fortgesetzt" {
		t.Fatalf("resume result wrong: %+v", res)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args.txt"))
	got := string(args)
	if !strings.Contains(got, "--resume") || !strings.Contains(got, "sess-123") {
		t.Fatalf("--resume <session_id> missing:\n%s", got)
	}
	if !strings.Contains(got, "Kunde: es war Chrome") {
		t.Fatalf("the resume input must be the new prompt:\n%s", got)
	}
	if strings.Contains(got, "ursprünglicher text") {
		t.Fatalf("on resume the old body must not be sent again:\n%s", got)
	}
	if strings.Contains(got, "--model") {
		t.Fatalf("without a model field --model must not be set:\n%s", got)
	}
}

func TestClaudeCodeAdapterBlocked(t *testing.T) {
	bin, dir := fakeClaude(t, `
cat <<'EOF'
{"type":"result","subtype":"success","session_id":"sess-9","result":"COVEY_STATUS: {\"status\":\"blocked\",\"correlation_key\":\"zammad:ticket:7\",\"question\":\"Warte auf Kunde\"}","total_cost_usd":0.05,"usage":{"input_tokens":5,"output_tokens":2}}
EOF`)
	adapter := &ClaudeCode{Binary: bin}
	res, err := adapter.Run(context.Background(), RunSpec{TaskID: "t", Title: "x", HomeDir: dir},
		func(string, json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "blocked" || res.CorrelationKey != "zammad:ticket:7" || res.SessionID != "sess-9" {
		t.Fatalf("blocked mapping wrong: %+v", res)
	}
}

func TestClaudeCodeAdapterExitError(t *testing.T) {
	bin, dir := fakeClaude(t, "exit 7")
	adapter := &ClaudeCode{Binary: bin}
	res, err := adapter.Run(context.Background(), RunSpec{TaskID: "t", Title: "x", HomeDir: dir},
		func(string, json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "failed" || !strings.Contains(res.Error, "exit") {
		t.Fatalf("exit ≠ 0 must yield failed: %+v", res)
	}
}

// TestClaudeCodeAdapterWorkDir secures the seam on which the sub-agent stands in
// the project checkout: cwd points into the project (only there does Claude Code
// find its CLAUDE.md, .claude/agents, skills), HOME stays the agent home (that
// is where ~/.claude, the wiki working copy and the caches live).
func TestClaudeCodeAdapterWorkDir(t *testing.T) {
	bin, home := fakeClaude(t, `
printf '%s\n' "$PWD" > "$HOME/cwd.txt"
cat <<'EOF'
{"type":"result","subtype":"success","session_id":"s","result":"fertig","total_cost_usd":0.01,"usage":{"input_tokens":1,"output_tokens":1}}
EOF`)
	project := filepath.Join(home, "repos", "p1-main")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	adapter := &ClaudeCode{Binary: bin}
	if _, err := adapter.Run(context.Background(), RunSpec{
		TaskID: "t", Title: "Auftrag", HomeDir: home, WorkDir: project,
	}, func(string, json.RawMessage) {}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(home, "cwd.txt"))
	if err != nil {
		t.Fatal(err)
	}
	// macOS: /var/… is a symlink to /private/var — suffix instead of equality.
	if cwd := strings.TrimSpace(string(got)); !strings.HasSuffix(cwd, filepath.Join("repos", "p1-main")) {
		t.Fatalf("cwd must lie inside the project checkout, was %q", cwd)
	}
}

// No runtime run inherits the daemon's COVEY_* variables — not even the outer
// one. Whatever a run needs, the caller passes explicitly (COVEY_ACTION_PORT,
// brokered key); nothing is inherited, otherwise the daemon token would be open
// to every subprocess.
func TestClaudeCodeAdapterDropsDaemonEnv(t *testing.T) {
	t.Setenv("COVEY_DAEMON_TOKEN", "daemon-jwt-geheim")
	t.Setenv("COVEY_AGENT_ID", "agent-1")
	bin, home := fakeClaude(t, `
printf 'token=%s agent=%s port=%s\n' "$COVEY_DAEMON_TOKEN" "$COVEY_AGENT_ID" "$COVEY_ACTION_PORT" > "$HOME/env.txt"
cat <<'EOF'
{"type":"result","subtype":"success","session_id":"s","result":"fertig"}
EOF`)
	adapter := &ClaudeCode{Binary: bin}
	if _, err := adapter.Run(context.Background(), RunSpec{
		TaskID: "t", Title: "x", HomeDir: home, Env: []string{"COVEY_ACTION_PORT=4711"},
	}, func(string, json.RawMessage) {}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(home, "env.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "token= agent= port=4711" {
		t.Fatalf("the daemon environment must not be inherited, the action port must arrive: %q", got)
	}
}

// Without a WorkDir everything stays as before: the outer run starts in the home.
func TestClaudeCodeAdapterWorkDirDefaultsToHome(t *testing.T) {
	bin, home := fakeClaude(t, `
printf '%s\n' "$PWD" > "$HOME/cwd.txt"
cat <<'EOF'
{"type":"result","subtype":"success","session_id":"s","result":"fertig"}
EOF`)
	adapter := &ClaudeCode{Binary: bin}
	if _, err := adapter.Run(context.Background(), RunSpec{TaskID: "t", Title: "x", HomeDir: home},
		func(string, json.RawMessage) {}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(home, "cwd.txt"))
	if cwd := strings.TrimSpace(string(got)); !strings.HasSuffix(cwd, filepath.Base(home)) {
		t.Fatalf("without a WorkDir the run must start in the home, was %q", cwd)
	}
}
