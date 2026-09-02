package daemon

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// The live test against a real educa instance. It SKIPS without a token, in the
// same spirit as the integration suite skipping without its database: a test
// that needs a credential must not fail on a machine that has none, and it must
// not be the only place the engine is covered either — everything structural is
// in runtime_educa_test.go and runs everywhere.
//
//	COVEY_EDUCA_TOKEN=… COVEY_EDUCA_MODEL=gemma-4-26B-A4B-it \
//	  go test ./internal/daemon -run TestEducaLive -v
//
// It costs tokens on a real contract, so it stays small: one task, one turn,
// the COVEY_STATUS line the compiled config demands — which is the actual
// question. Whether the harness runs at all against this endpoint is answered
// by the first run; everything after it is detail.
func TestEducaLive(t *testing.T) {
	token := strings.TrimSpace(os.Getenv("COVEY_EDUCA_TOKEN"))
	if token == "" {
		t.Skip("COVEY_EDUCA_TOKEN is not set — the live test needs a real credential")
	}
	model := strings.TrimSpace(os.Getenv("COVEY_EDUCA_MODEL"))
	if model == "" {
		model = "gemma-4-26B-A4B-it"
	}

	e := NewEduca()
	home := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var events int
	res, err := e.Run(ctx, RunSpec{
		TaskID: "live-1",
		Title:  "Selbsttest",
		Body: "Answer with the single word `bereit` and nothing else. Do not use any tool.\n" +
			"Then close with exactly this line:\n" +
			`COVEY_STATUS: {"status":"done","result":"bereit"}`,
		SystemPrompt: "You are a covey agent under test. Be terse.",
		Model:        model,
		// Declared levels are only worth as much as a run that carries one:
		// COVEY_EDUCA_EFFORT=high exercises the flag against the gateway.
		Effort:       strings.TrimSpace(os.Getenv("COVEY_EDUCA_EFFORT")),
		AllowedTools: []string{"Read"},
		MaxTurns:     3,
		HomeDir:      home,
		Env:          []string{"ANTHROPIC_AUTH_TOKEN=" + token},
	}, func(kind string, payload json.RawMessage) { events++ })
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	t.Logf("status=%q result=%q error=%q session=%q model=%q events=%d",
		res.Status, res.Result, res.Error, res.SessionID, res.Model, events)
	t.Logf("tokens: in=%d out=%d cacheRead=%d cacheWrite=%d cost=%v",
		res.InputTokens, res.OutputTokens, res.CacheReadTokens, res.CacheCreationTokens, res.CostUSD)

	if res.Status != "done" {
		t.Fatalf("the harness did not complete a run against educa: %+v", res)
	}
	if res.CostUSD != 0 {
		t.Fatalf("no amount may be inherited from the harness: %v", res.CostUSD)
	}
	if res.SessionID == "" {
		t.Error("without a session id the blocked→working edge cannot resume")
	}
	if res.InputTokens+res.OutputTokens == 0 {
		t.Error("the run reports no tokens — the platform's estimate has nothing to work with")
	}
}

// The two properties the declaration rests on, measured rather than reasoned
// about: that the harness executes TOOLS against this endpoint, and that a
// session can be RESUMED — the whole `blocked` mechanism hangs off the latter
// (spec/03), and the descriptor claims it.
func TestEducaLiveToolsAndResume(t *testing.T) {
	token := strings.TrimSpace(os.Getenv("COVEY_EDUCA_TOKEN"))
	if token == "" {
		t.Skip("COVEY_EDUCA_TOKEN is not set — the live test needs a real credential")
	}
	model := strings.TrimSpace(os.Getenv("COVEY_EDUCA_MODEL"))
	if model == "" {
		model = "gemma-4-26B-A4B-it"
	}

	e := NewEduca()
	home := t.TempDir()
	if err := os.WriteFile(home+"/losung.txt", []byte("apfelbaum\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	base := RunSpec{
		SystemPrompt: "You are a covey agent under test. Be terse.",
		Model:        model,
		AllowedTools: []string{"Bash", "Read", "Glob", "Grep"},
		MaxTurns:     8,
		HomeDir:      home,
		Env:          []string{"ANTHROPIC_AUTH_TOKEN=" + token},
	}

	first := base
	first.TaskID = "live-tools"
	first.Title = "Datei lesen"
	first.Body = "Read the file losung.txt in the current directory and report the word it contains.\n" +
		"Then close with exactly this line, with the word in place of WORT:\n" +
		`COVEY_STATUS: {"status":"done","result":"WORT"}`

	res, err := e.Run(ctx, first, func(string, json.RawMessage) {})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Logf("tools: status=%q result=%q error=%q session=%q", res.Status, res.Result, res.Error, res.SessionID)
	if res.Status != "done" {
		t.Fatalf("the tool run did not complete: %+v", res)
	}
	if !strings.Contains(strings.ToLower(res.Result), "apfelbaum") {
		t.Errorf("the agent did not actually read the file: %q", res.Result)
	}
	if res.SessionID == "" {
		t.Fatal("no session id — resume cannot be tested")
	}

	// Second phase: the same session continues. This is the edge a blocked
	// agent takes when the event it waited for arrives.
	second := base
	second.TaskID = "live-resume"
	second.ResumeSessionID = res.SessionID
	second.ResumeInput = "Which word did you just report? Answer with that word only, then close with\n" +
		`COVEY_STATUS: {"status":"done","result":"WORT"}` + "\nwith the word in place of WORT."

	res2, err := e.Run(ctx, second, func(string, json.RawMessage) {})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	t.Logf("resume: status=%q result=%q error=%q session=%q", res2.Status, res2.Result, res2.Error, res2.SessionID)
	if res2.Status != "done" {
		t.Fatalf("the resumed run did not complete: %+v", res2)
	}
	if !strings.Contains(strings.ToLower(res2.Result), "apfelbaum") {
		t.Errorf("the session did not carry its context across the resume: %q", res2.Result)
	}
}
