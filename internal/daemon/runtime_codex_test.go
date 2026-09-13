package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeCodex writes a CLI that records its arguments and then prints whatever
// the test wants on stdout. What is under test is the adapter, not Codex.
func fakeCodex(t *testing.T, script string) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin = filepath.Join(dir, "codex")
	content := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + dir + "/args.txt\"\n" + script + "\n"
	if err := os.WriteFile(bin, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

// The adapter reads Codex's JSON event stream and turns it into a run result.
// Every line is passed on as a runtime event — that is what fills the
// recording — and the last piece of text is what the task ends up saying.
func TestCodexReadsItsEventStream(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	bin, dir := fakeCodex(t, `
cat <<'JSON'
{"type":"agent_message","text":"Erst denke ich nach."}
nicht json, und wird übersprungen
{"type":"agent_message","text":"Dann das Ergebnis."}
{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":30,"reasoning_output_tokens":5}}
JSON`)

	var events int
	c := &Codex{Binary: bin}
	res, err := c.Run(context.Background(), RunSpec{Body: "Tu etwas", WorkDir: dir},
		func(kind string, payload json.RawMessage) {
			if kind == "runtime" {
				events++
			}
		})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "done" {
		t.Fatalf("the run ended as %q: %s", res.Status, res.Error)
	}
	// The prose line is not an event: everything that is not JSON is skipped
	// rather than recorded as a runtime message nobody can read.
	if events != 3 {
		t.Errorf("%d runtime events, expected 3", events)
	}
	if !strings.Contains(res.Result, "Dann das Ergebnis.") {
		t.Errorf("the result is %q", res.Result)
	}
	// The token counts come from the engine, which is what makes the cost
	// attributable to a run rather than estimated from a length.
	if res.InputTokens == 0 || res.OutputTokens == 0 {
		t.Errorf("no tokens were read: in=%d out=%d", res.InputTokens, res.OutputTokens)
	}
	// The cached input is kept apart from the fresh one, because the two are
	// priced differently — adding them together would cost a cache read like
	// a fresh token.
	if res.CacheReadTokens != 20 {
		t.Errorf("the cached input is %d — it is priced differently and must stay apart", res.CacheReadTokens)
	}
	// Reasoning tokens are billed as output and belong there; Codex has no
	// counterpart to a cache WRITE, so that stays zero rather than being
	// filled with something plausible.
	if res.OutputTokens != 35 {
		t.Errorf("output tokens = %d, expected output plus reasoning", res.OutputTokens)
	}
	if res.CacheCreationTokens != 0 {
		t.Errorf("a cache write was invented: %d", res.CacheCreationTokens)
	}

	args, err := os.ReadFile(filepath.Join(dir, "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "exec") || !strings.Contains(string(args), "--json") {
		t.Errorf("the adapter called codex with %q", args)
	}
}

func TestCodexPassesTheModelThrough(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	bin, dir := fakeCodex(t, `echo '{"type":"agent_message","text":"ok"}'`)
	c := &Codex{Binary: bin}
	if _, err := c.Run(context.Background(), RunSpec{Body: "x", Model: "o4-mini", WorkDir: dir},
		func(string, json.RawMessage) {}); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args.txt"))
	if !strings.Contains(string(args), "--model") || !strings.Contains(string(args), "o4-mini") {
		t.Errorf("the model did not reach the CLI: %q", args)
	}
}

// Continuing a session is not verified for this engine, and the control plane
// should never ask. If it does anyway, the adapter says so plainly rather than
// starting a fresh run that silently loses the conversation it was meant to
// continue.
func TestCodexRefusesToResume(t *testing.T) {
	c := &Codex{Binary: "codex-gibt-es-hier-nicht"}
	res, err := c.Run(context.Background(), RunSpec{Body: "weiter", ResumeSessionID: "s-1"},
		func(string, json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "failed" {
		t.Errorf("resuming ended as %q", res.Status)
	}
	if !strings.Contains(res.Error, "resumed") {
		t.Errorf("the refusal does not say what it refused: %q", res.Error)
	}
}

// A binary that is not in the sandbox is the single most common setup fault,
// and the message names it rather than reporting a run that failed.
func TestCodexWithoutItsBinary(t *testing.T) {
	c := &Codex{Binary: filepath.Join(t.TempDir(), "gibtesnicht")}
	res, err := c.Run(context.Background(), RunSpec{Body: "x"}, func(string, json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "failed" {
		t.Errorf("a missing binary ended as %q", res.Status)
	}
	if !strings.Contains(res.Error, "sandbox") {
		t.Errorf("the message does not name the cause: %q", res.Error)
	}
}
