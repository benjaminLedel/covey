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

// The last open point of spec/25 is not plumbing but work: "a run that completes
// is not a run that works". Everything in runtime_sevencode_test.go drives a fake
// binary, which proves the adapter says the right words and reads back what it was
// told — it cannot say whether an agent on this harness gets a job done.
//
//	COVEY_SEVENCODE_TOKEN=… [COVEY_SEVENCODE_MODEL=… SEVENCODE_API_BASE=…] \
//	  go test ./internal/daemon -run TestSevenCodeLiveRealisticTask -v -timeout 20m
//
// The task is the one TestEducaLiveRealisticTask runs, deliberately and to the
// byte: this engine is interesting only as a second agent loop in front of the
// SAME gateway (spec/23), so the two harnesses are comparable by nothing else than
// an identical brief. Change the fixture and you break the comparison — both files
// carry the same statement, keep them the same.
//
// Beyond what the educa run checks, two claims of spec/25 can only be settled
// against the real CLI: that a run measures (the usage lines are per turn and the
// adapter sums them), and that the session lands in the agent home, which is where
// a resumed run has to find it.
func TestSevenCodeLiveRealisticTask(t *testing.T) {
	token := strings.TrimSpace(os.Getenv("COVEY_SEVENCODE_TOKEN"))
	if token == "" {
		t.Skip("COVEY_SEVENCODE_TOKEN is not set — the live test needs a real credential")
	}
	// Deliberately NOT defaulted here: unset means unset, and then the run has to
	// land on the CLI's own default. That is the setting most agents on this engine
	// will actually have, and which models exist belongs to the instance behind it
	// (spec/23), not to this file.
	model := strings.TrimSpace(os.Getenv("COVEY_SEVENCODE_MODEL"))

	// Which of the two programs that answer to this name stands on this host
	// decides whether the run proves anything at all. The npm build does not reject
	// a flag it does not know — it ignores it and exits 0 (spec/25, #279) — so
	// against 0.0.x this test would complete, look measured and have driven
	// something else. Asked of the binary rather than assumed, and it skips rather
	// than fails: a host with the other build installed is not wrong, it is just
	// not this one.
	bin := strings.TrimSpace(os.Getenv(sevencodeBinEnv))
	if bin == "" {
		bin = sevencodeDefaultBinary
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		t.Skipf("%s is not on this host — the live test runs the real CLI (or point %s at one)", bin, sevencodeBinEnv)
	}
	if out, err := exec.Command(path, "--version").Output(); err == nil {
		version := ""
		for _, line := range strings.Split(string(out), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				version = line
				break
			}
		}
		if strings.HasPrefix(version, "0.") {
			t.Skipf("%s is the npm build (%s) — an opencode program with the same name, "+
				"which would ignore these flags and exit 0 (spec/25)", path, version)
		}
		t.Logf("cli: %s (%s)", path, version)
	}

	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not available — the trial task needs it")
	}

	// Home and work stay separate, as in a real sandbox: the code under test sits
	// in the work directory the run starts in, while SEVENCODE_HOME is pinned at
	// the agent home (src/config/paths.ts:42) — which is also what the session
	// check below reads, so a run whose history landed anywhere else is caught here
	// rather than on the next wake, when the resume finds nothing.
	home := t.TempDir()
	work := t.TempDir()

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
		if err := os.WriteFile(filepath.Join(work, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("stats.py", buggy)
	write("check.py", check)

	sum := func(name string) string {
		b, err := os.ReadFile(filepath.Join(work, name))
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(b)
		return hex.EncodeToString(h[:])
	}
	checkBefore := sum("check.py")

	// The endpoint is not covey's to name and this test does not invent one
	// (spec/25): childEnv passes the host's SEVENCODE_API_BASE through, and where
	// that is unset the CLI answers from its own default. The run itself never says
	// where it worked, so the line below is the only trace of it — and a token
	// spent at an endpoint nobody meant is exactly the finding worth having.
	base := strings.TrimSpace(os.Getenv("SEVENCODE_API_BASE"))
	if base == "" {
		base = "(unset — the CLI's own default)"
	}

	e := NewSevenCode()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	var turns int
	var stream strings.Builder
	start := time.Now()
	res, err := e.Run(ctx, RunSpec{
		TaskID: "trial-1",
		Title:  "check.py schlägt fehl",
		Body: "In this directory there is `stats.py` and `check.py`. `python3 check.py` fails.\n" +
			"Find the cause, fix `stats.py`, and run `python3 check.py` again until it passes.\n" +
			"Do not change `check.py` — it states what is expected.\n" +
			"When you are done, report in one sentence what was wrong.",
		// The platform's own share of the prompt, verbatim. On this engine it shares
		// the one message with the task — there is no flag for a system prompt
		// (spec/25) — so this run is also the measurement of what ~9 KB of platform
		// text in front of a small task does to the answer.
		SystemPrompt: "You are Testfried, a developer agent at covey.\n\n" + agents.ProtocolInstructions,
		Model:        model,
		// Both inert on this engine and passed anyway: the CLI has neither a turn
		// limit nor a tool scope (open point 3 of spec/25), and a live test that hid
		// that would leave the false impression that a bounded run happened.
		AllowedTools: DefaultAllowedTools,
		MaxTurns:     25,
		HomeDir:      home,
		WorkDir:      work,
		Env:          []string{"SEVENCODE_API_KEY=" + token},
	}, func(_ string, payload json.RawMessage) {
		turns++
		stream.Write(payload)
		stream.WriteByte('\n')
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	elapsed := time.Since(start)

	t.Logf("model=%q base=%s status=%q duration=%s events=%d", model, base, res.Status,
		elapsed.Round(time.Second), turns)
	t.Logf("result: %s", strings.TrimSpace(res.Result))
	if res.Error != "" {
		t.Logf("error: %s", res.Error)
	}
	t.Logf("tokens: in=%d out=%d", res.InputTokens, res.OutputTokens)
	// Whether the closing line arrived is not the verdict — a run without it is
	// done with its whole text either way — but this engine is asked for it inside
	// the same message as the task, and whether it answers there is worth reading
	// once per model rather than arguing about.
	if !strings.Contains(stream.String(), statusMarker) {
		t.Logf("no %s line in the stream: the run counts as done with its whole text", statusMarker)
	}

	// The only verdict that counts: does the code work now, judged by running it
	// rather than by reading the agent's summary.
	out, runErr := exec.CommandContext(ctx, py, filepath.Join(work, "check.py")).CombinedOutput()
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
	if res.Error != "" {
		t.Errorf("the code is fixed, but the CLI reported a problem: %s", res.Error)
	}

	// The two claims only a real run settles. A run that brought back no figure
	// would book nothing for a seat that consumed something (spec/13), and a run
	// whose session is not in the agent home cannot be resumed — the resume is what
	// makes blocked agents open on this engine (spec/03), and the store is the only
	// place its id is ever read from.
	if !res.Measured() {
		t.Error("the run reported no tokens, so nothing was summed from its usage lines")
	}
	if res.SessionID == "" {
		t.Error("the run brought back no session id — the store was not found")
	} else if !sevencodeHasSession(RunSpec{HomeDir: home}, res.SessionID) {
		t.Errorf("the session %s is not in this agent home, so the next wake could not resume it", res.SessionID)
	}
}
