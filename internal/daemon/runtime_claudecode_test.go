package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeClaude erzeugt ein Fake-`claude`-Binary, das seine Argumente nach
// args.txt schreibt und ein vorgegebenes stream-json-Protokoll emittiert.
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
		t.Fatalf("result falsch: %+v", res)
	}
	if res.SessionID != "sess-123" {
		t.Fatalf("session_id fehlt: %+v", res)
	}
	if res.CostUSD != 0.42 || res.InputTokens != 100 {
		t.Fatalf("kosten falsch: %+v", res)
	}
	if len(events) != 3 {
		t.Fatalf("alle stream-json-Zeilen müssen als Events fließen, got %d", len(events))
	}

	args, err := os.ReadFile(filepath.Join(dir, "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(args)
	for _, want := range []string{"-p", "Ticket 42", "--output-format", "stream-json",
		"--append-system-prompt", "Du bist der Support-Agent.", "--allowedTools", "Bash,Read",
		"--max-turns", "30", "--dangerously-skip-permissions"} {
		if !strings.Contains(got, want) {
			t.Fatalf("flag %q fehlt in args:\n%s", want, got)
		}
	}
	if strings.Contains(got, "--resume") {
		t.Fatal("ohne resume-Session darf --resume nicht gesetzt sein")
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
		t.Fatalf("resume-result falsch: %+v", res)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args.txt"))
	got := string(args)
	if !strings.Contains(got, "--resume") || !strings.Contains(got, "sess-123") {
		t.Fatalf("--resume <session_id> fehlt:\n%s", got)
	}
	if !strings.Contains(got, "Kunde: es war Chrome") {
		t.Fatalf("resume-input muss der neue Prompt sein:\n%s", got)
	}
	if strings.Contains(got, "ursprünglicher text") {
		t.Fatalf("beim Resume darf der alte Body nicht erneut gesendet werden:\n%s", got)
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
		t.Fatalf("blocked-mapping falsch: %+v", res)
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
		t.Fatalf("exit ≠ 0 muss failed liefern: %+v", res)
	}
}
