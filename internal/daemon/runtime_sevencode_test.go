package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSevenCode writes a CLI that records its arguments and its own environment
// and then runs the given shell snippet. It stands in for the real binary the way
// fakeClaude does — the adapter is what is under test here, not SevenCode.
//
// The arguments are recorded NUL-separated rather than one per line, because the
// prompt IS multi-line: a line-based record would break the message into several
// "arguments" and the test would then be asserting where the newlines fell.
func fakeSevenCode(t *testing.T, script string) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin = filepath.Join(dir, "sevencode")
	content := "#!/bin/sh\n: > \"" + dir + "/args.bin\"\n" +
		"for a in \"$@\"; do printf '%s' \"$a\" >> \"" + dir + "/args.bin\"; printf '\\0' >> \"" +
		dir + "/args.bin\"; done\n" +
		"env > \"" + dir + "/env.txt\"\n" + script + "\n"
	if err := os.WriteFile(bin, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

// fakeArgs reads back what the CLI was called with.
func fakeArgs(t *testing.T, dir string) []string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, "args.bin"))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(body), "\x00")
	return parts[:len(parts)-1]
}

func fakeEnv(t *testing.T, dir string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, "env.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// runSevenCode drives the adapter against a fake binary and returns the result,
// the recorded event lines, and the arguments the CLI saw.
func runSevenCode(t *testing.T, script string, spec RunSpec) (RunResult, []string, []json.RawMessage) {
	t.Helper()
	bin, dir := fakeSevenCode(t, script)
	t.Setenv(sevencodeBinEnv, bin)
	// Both directories have to exist: the run starts in the work directory and
	// writes its session under the home, so a path that is only a path fails in a
	// way that has nothing to do with the adapter.
	if spec.HomeDir == "" {
		spec.HomeDir = mkDir(t, "home")
	}
	if spec.WorkDir == "" {
		spec.WorkDir = mkDir(t, "work")
	}
	var events []json.RawMessage
	res, err := NewSevenCode().Run(context.Background(), spec,
		func(_ string, payload json.RawMessage) { events = append(events, payload) })
	if err != nil {
		t.Fatal(err)
	}
	return res, fakeArgs(t, dir), events
}

// The event stream as the 1.0 line emits it: one JSON object per line, written
// unchanged by `--json` (src/headless.ts:115), field names from the union at
// src/core/types.ts:186-424. The usage figures are PER TURN, so the two lines
// below are the run's sum rather than its last word, and the text arrives in two
// deltas with the closing status line inside the last of them.
const sevencodeStream = `cat <<'EOF'
{"type":"turn_start","turn":1,"model":"sevenai-qwen","reasoningEffort":""}
{"type":"tool_start","id":"t1","name":"bash","input":{"command":"pytest"},"summary":"pytest"}
{"type":"tool_end","id":"t1","name":"bash","output":"2 passed","isError":false,"durationMs":12}
{"type":"thinking_delta","text":"Ich sehe mir den fehlenden Test an."}
{"type":"usage","usage":{"inputTokens":42,"outputTokens":7,"reasoningTokens":3}}
{"type":"text_delta","text":"Erledigt.\n"}
{"type":"usage","usage":{"inputTokens":8,"outputTokens":5}}
{"type":"text_delta","text":"COVEY_STATUS: {\"status\":\"done\",\"result\":\"Ticket beantwortet\",\"memory\":\"Kunde nutzt Firefox\"}"}
{"type":"done","turns":2,"stoppedBy":"model"}
EOF`

func TestSevenCodeIsRegistered(t *testing.T) {
	d, ok := Describe("sevencode")
	if !ok {
		t.Fatal("sevencode is not registered")
	}
	// One credential: the token as a variable. A login file would be the form the
	// opencode build needs; this CLI takes its access from the environment
	// (SEVENCODE_API_KEY), and a credential written where nothing reads it is a
	// credential that is not there.
	if !d.NeedsCredential() || len(d.Credentials) != 1 {
		t.Fatalf("credentials: %+v", d.Credentials)
	}
	api, _ := d.Credential(CredAPIKey)
	if api.EnvVar != "SEVENCODE_API_KEY" || api.Path != "" {
		t.Fatalf("the token is brokered as the variable the CLI reads: %+v", api)
	}
	if !d.Capabilities.Resume {
		t.Fatal("the CLI continues a stored session, so blocking agents are open here")
	}
	if d.Capabilities.SkillsDir != ".sevencode/skills" {
		t.Fatalf("skills: %q", d.Capabilities.SkillsDir)
	}
	// Neither an effort lever nor a model list: `--model` is the only lever, and
	// which models exist belongs to the instance behind it (spec/23). An offered
	// choice the CLI cannot act on turns into a run-time error.
	if len(d.Capabilities.EffortLevels) != 0 || len(d.Capabilities.Models) != 0 {
		t.Fatalf("one lever only: %+v %+v", d.Capabilities.EffortLevels, d.Capabilities.Models)
	}
	if len(SevenCode{}.Prices()) != 0 {
		t.Fatal("the stream carries no money field, so nothing can be priced here")
	}
}

func TestSevenCodeRunsTheMeasuredSurface(t *testing.T) {
	res, args, events := runSevenCode(t, sevencodeStream, RunSpec{
		Title:        "Prüfen",
		Body:         "Der Test schlägt fehl.",
		SystemPrompt: "Du bist QA.",
	})

	// The flags first and the prompt last, in the one position where it cannot be
	// read as an option and no option can be read as part of it.
	if args[0] != "--json" {
		t.Fatalf("the run speaks JSON from the first argument on: %v", args)
	}
	if !hasArg(args, "--auto") {
		t.Fatalf("without a mode the CLI decides what a run with no terminal may do: %v", args)
	}
	if args[len(args)-2] != "-p" {
		t.Fatalf("`-p` takes the prompt and the pair arrives last: %v", args)
	}
	prompt := args[len(args)-1]
	if !strings.Contains(prompt, "Du bist QA.") || !strings.Contains(prompt, "Der Test schlägt fehl.") {
		t.Fatalf("the config goes in front of the task in the one message: %q", prompt)
	}

	if len(events) != 9 {
		t.Fatalf("every line belongs in the recording as it came: %d", len(events))
	}
	if res.Status != "done" || res.Result != "Ticket beantwortet" || res.Memory != "Kunde nutzt Firefox" {
		t.Fatalf("the closing line decides what the run was: %+v", res)
	}
	// The per-turn figures summed. What the CLI counts apart as reasoning tokens
	// has no field in a RunResult and stays in the recording, where the line that
	// carried it stands.
	if res.InputTokens != 50 || res.OutputTokens != 12 {
		t.Fatalf("the usage lines are summed, not taken last: %+v", res)
	}
	if !res.Measured() {
		t.Fatal("a run that brought tokens back is not an unmeasured one")
	}
}

func TestSevenCodeModelPassesThroughAndEffortDoesNot(t *testing.T) {
	_, args, _ := runSevenCode(t, sevencodeStream, RunSpec{
		Model:  "gpt-oss-120b",
		Effort: "high",
	})
	if !hasArg(args, "--model") || !hasArg(args, "gpt-oss-120b") {
		t.Fatalf("the model goes in as --model: %v", args)
	}
	// This CLI has no lever for a reasoning effort. Sending one anyway would be the
	// spec/19 mistake in the other direction: an argument the binary does not know
	// is not rejected but ignored, so the setting would look chosen and be nothing.
	for _, a := range args {
		if strings.HasPrefix(a, "--variant") || strings.HasPrefix(a, "--reasoning") {
			t.Fatalf("no effort flag exists here, so none may be sent: %v", args)
		}
	}
}

// sessionFileIn writes a session the way the CLI stores one and returns its id.
// The project directory's own name is irrelevant to the adapter on purpose.
func sessionFileIn(t *testing.T, home, project, id string) string {
	t.Helper()
	dir := filepath.Join(home, ".sevencode", "projects", project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "2026-08-27T09-14-02-881-" + id + ".jsonl"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSevenCodeResumesAKnownSession(t *testing.T) {
	home := t.TempDir()
	id := sessionFileIn(t, home, "-work", "hset8dsq")

	res, args, _ := runSevenCode(t, sevencodeStream, RunSpec{
		HomeDir:         home,
		ResumeSessionID: id,
		ResumeInput:     "Der Termin ist vorüber — fahre fort.",
	})

	if !hasArg(args, "--resume") || !hasArg(args, id) {
		t.Fatalf("the session the CLI knows is handed to it: %v", args)
	}
	// A resumed run speaks into a session that already carries the config, so the
	// message is the event text and not the config a second time.
	if args[len(args)-1] != "Der Termin ist vorüber — fahre fort." {
		t.Fatalf("the config is not repeated into a session that has it: %q", args[len(args)-1])
	}
	if res.Status != "done" {
		t.Fatalf("a resumed run finishes like any other: %+v", res)
	}
	// The stream never names the session, so the answer comes from where the CLI
	// keeps it.
	if res.SessionID != id {
		t.Fatalf("the run's own session: %+v", res)
	}
}

// An unknown session id is answered by the CLI with success and an empty answer
// (src/index.ts:411). Kept as a run, that is a task recorded as finished having
// said nothing — so the store is asked before the child starts.
func TestSevenCodeRefusesAResumeTheCLIWouldNotFind(t *testing.T) {
	bin, dir := fakeSevenCode(t, sevencodeStream)
	t.Setenv(sevencodeBinEnv, bin)

	res, err := NewSevenCode().Run(context.Background(), RunSpec{
		HomeDir:         t.TempDir(),
		WorkDir:         t.TempDir(),
		ResumeSessionID: "ses_abc",
		ResumeInput:     "weiter",
	}, func(string, json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "failed" || !strings.Contains(res.Error, "ses_abc") {
		t.Fatalf("a resume the CLI cannot find has to say so: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "args.bin")); err == nil {
		t.Fatal("the child must not start at all — an empty run is the failure being prevented")
	}
}

func TestSevenCodeErrorMessageBeatsTheExitCode(t *testing.T) {
	res, _, _ := runSevenCode(t, `cat <<'EOF'
{"type":"error","message":"Kein Zugang für diese Instanz.","retryable":false}
EOF
exit 1`, RunSpec{})
	if res.Status != "failed" || !strings.Contains(res.Error, "Kein Zugang für diese Instanz.") {
		t.Fatalf("the CLI's own sentence belongs in the task: %+v", res)
	}
}

// `done` names what ended the run, and `turns` or `truncated` mean work was left
// (src/core/types.ts:409). The limit itself cannot be set on this engine, so what
// is left is not to write a cut-off run into the record as a finished one.
func TestSevenCodeATurnLimitIsNotSilence(t *testing.T) {
	res, _, _ := runSevenCode(t, `cat <<'EOF'
{"type":"text_delta","text":"Ich arbeite noch —"}
{"type":"done","turns":2,"stoppedBy":"turns"}
EOF`, RunSpec{})
	if res.Status != "failed" {
		t.Fatalf("a run that was cut off did not finish: %+v", res)
	}
	if !strings.Contains(res.Error, "turns") || !strings.Contains(res.Error, "2") {
		t.Fatalf("the reason names the limit and the turns it used: %q", res.Error)
	}
}

func TestSevenCodeEnvironment(t *testing.T) {
	home := t.TempDir()
	// The daemon's own variables must not reach the child, and the brokered token
	// must not be buried by anything the adapter appends afterwards.
	t.Setenv("COVEY_DAEMON_TOKEN", "daemon-secret")

	bin, dir := fakeSevenCode(t, sevencodeStream)
	t.Setenv(sevencodeBinEnv, bin)
	if _, err := NewSevenCode().Run(context.Background(), RunSpec{
		HomeDir: home,
		WorkDir: t.TempDir(),
		Env:     []string{"SEVENCODE_API_KEY=tok-brokered"},
	}, func(string, json.RawMessage) {}); err != nil {
		t.Fatal(err)
	}
	env := fakeEnv(t, dir)

	if !strings.Contains(env, "HOME="+home+"\n") {
		t.Errorf("the home stays the agent's own:\n%s", env)
	}
	// The session history that makes a resume possible lives under this variable.
	if !strings.Contains(env, "SEVENCODE_HOME="+filepath.Join(home, ".sevencode")+"\n") {
		t.Errorf("the CLI's own directory has to sit in the home that survives:\n%s", env)
	}
	// A CLI that speaks its interface language would write German prose into an
	// English record.
	if !strings.Contains(env, "SEVENCODE_LANG=en\n") {
		t.Errorf("the transcript should not depend on the image's locale:\n%s", env)
	}
	// A harness that updates itself stops being the one the run was recorded
	// against — and the layer it starts from cannot be written to.
	if !strings.Contains(env, "SEVENCODE_NO_AUTO_UPDATE=1\n") {
		t.Errorf("the engine layer is pinned and read-only:\n%s", env)
	}
	if !strings.Contains(env, "SEVENCODE_API_KEY=tok-brokered\n") {
		t.Errorf("the brokered credential has to arrive: %s", env)
	}
	if strings.Contains(env, "COVEY_DAEMON_TOKEN") {
		t.Errorf("the daemon's own token belongs to the daemon:\n%s", env)
	}
}

// A line that is not JSON is not an error: the recording has it either way, and a
// CLI is allowed to say something on its way past.
func TestSevenCodeToleratesAPlainLine(t *testing.T) {
	res, _, events := runSevenCode(t, `printf 'warning: experimental sqlite\n'
cat <<'EOF'
{"type":"text_delta","text":"Fertig."}
{"type":"done","turns":1,"stoppedBy":"model"}
EOF`, RunSpec{})
	if len(events) != 3 {
		t.Fatalf("the plain line is recorded too: %d", len(events))
	}
	if res.Status != "done" || res.Result != "Fertig." {
		t.Fatalf("%+v", res)
	}
}

// mkDir makes a directory under the test's temporary root.
func mkDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
