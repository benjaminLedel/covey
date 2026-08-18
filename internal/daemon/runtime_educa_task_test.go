package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"covey/internal/agents"
)

// Whether the plumbing holds and whether an agent can WORK are two questions,
// and the first one flatters the second. TestEducaLive answers "does a run
// complete"; this one answers "does a Covey agent get a job done on these
// models" — with the platform's real prompt (agents.ProtocolInstructions, ~9 KB),
// the default tool scope, several turns, and a result somebody can check without
// believing the agent's own report.
//
//	COVEY_EDUCA_TOKEN=… COVEY_EDUCA_MODEL=gpt-oss-120b \
//	  go test ./internal/daemon -run TestEducaLiveRealisticTask -v -timeout 20m
//
// The task is small but genuinely multi-step: a failing test, a bug in the code
// under it, a fix, and a re-run to prove it. The check afterwards is deliberately
// hostile — the test file must be UNCHANGED, because deleting the test is the
// cheapest way to make it pass.
func TestEducaLiveRealisticTask(t *testing.T) {
	token := strings.TrimSpace(os.Getenv("COVEY_EDUCA_TOKEN"))
	if token == "" {
		t.Skip("COVEY_EDUCA_TOKEN is not set — the live test needs a real credential")
	}
	// Deliberately NOT defaulted here: unset means unset, and then the run has
	// to land on the engine's own default. That is the setting most agents will
	// actually have.
	model := strings.TrimSpace(os.Getenv("COVEY_EDUCA_MODEL"))
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not available — the trial task needs it")
	}

	home := t.TempDir()

	// The bug: for an even-length list the median has to be the mean of the two
	// middle values. This returns the upper one.
	const buggy = `def median(values):
    ordered = sorted(values)
    middle = len(ordered) // 2
    return ordered[middle]


def mean(values):
    return sum(values) / len(values)
`
	const check = `from stats import median, mean

cases = [
    ([3, 1, 2], 2),
    ([4, 1, 3, 2], 2.5),
    ([10, 20], 15),
    ([5], 5),
]
for values, want in cases:
    got = median(values)
    assert got == want, f"median({values}) = {got}, expected {want}"
assert mean([1, 2, 3]) == 2
print("all checks passed")
`
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(home, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("stats.py", buggy)
	write("check.py", check)

	sum := func(name string) string {
		b, err := os.ReadFile(filepath.Join(home, name))
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(b)
		return hex.EncodeToString(h[:])
	}
	checkBefore := sum("check.py")

	e := NewEduca()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	var turns int
	start := time.Now()
	res, err := e.Run(ctx, RunSpec{
		TaskID: "trial-1",
		Title:  "check.py schlägt fehl",
		Body: "In this directory there is `stats.py` and `check.py`. `python3 check.py` fails.\n" +
			"Find the cause, fix `stats.py`, and run `python3 check.py` again until it passes.\n" +
			"Do not change `check.py` — it states what is expected.\n" +
			"When you are done, report in one sentence what was wrong.",
		// The platform's own share of the prompt, verbatim — this is what every
		// agent actually carries, and it is most of what a small model has to
		// cope with before it gets to the task.
		SystemPrompt: "You are Testfried, a developer agent at Covey.\n\n" + agents.ProtocolInstructions,
		Model:        model,
		AllowedTools: DefaultAllowedTools,
		MaxTurns:     25,
		HomeDir:      home,
		Env:          []string{"ANTHROPIC_AUTH_TOKEN=" + token},
	}, func(kind string, payload json.RawMessage) { turns++ })
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	elapsed := time.Since(start)

	t.Logf("model=%s status=%q duration=%s events=%d", model, res.Status, elapsed.Round(time.Second), turns)
	t.Logf("result: %s", strings.TrimSpace(res.Result))
	if res.Error != "" {
		t.Logf("error: %s", res.Error)
	}
	t.Logf("tokens: in=%d out=%d", res.InputTokens, res.OutputTokens)

	// The only verdict that counts: does the code work now, judged by running it
	// rather than by reading the agent's summary.
	out, runErr := exec.CommandContext(ctx, py, filepath.Join(home, "check.py")).CombinedOutput()
	t.Logf("python3 check.py → %v\n%s", runErr, strings.TrimSpace(string(out)))

	if sum("check.py") != checkBefore {
		t.Fatal("the agent changed the test instead of the code — that is not a fix")
	}
	if runErr != nil {
		t.Fatalf("the bug is still there after the run (status was %q)", res.Status)
	}
	if res.Status != "done" {
		t.Errorf("the code is fixed, but the run did not close with status done: %q / %q", res.Status, res.Error)
	}
}
