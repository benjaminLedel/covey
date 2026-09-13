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
// and then runs the given shell snippet. It stands in for the real binary the
// way fakeClaude does — the adapter is what is under test here, not SevenCode.
func fakeSevenCode(t *testing.T, script string) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin = filepath.Join(dir, "sevencode")
	content := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + dir + "/args.txt\"\n" +
		"env > \"" + dir + "/env.txt\"\n" + script + "\n"
	if err := os.WriteFile(bin, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

// The event stream as the CLI actually emits it — one JSON line per event,
// common envelope, the figures in `step_finish`. Copied from a measured run
// against a local OpenAI-compatible double rather than invented, which is the
// whole point of this file after the first draft was written against flags the
// binary does not have.
const sevencodeStream = `cat <<'EOF'
{"type":"step_start","timestamp":1,"sessionID":"ses_abc","part":{"type":"step-start"}}
{"type":"text","timestamp":2,"sessionID":"ses_abc","part":{"type":"text","text":"Erledigt.\nCOVEY_STATUS: {\"status\":\"done\",\"result\":\"Ticket beantwortet\",\"memory\":\"Kunde nutzt Firefox\"}"}}
{"type":"step_finish","timestamp":3,"sessionID":"ses_abc","part":{"type":"step-finish","reason":"stop","cost":0.0125,"tokens":{"total":49,"input":42,"output":7,"reasoning":0,"cache":{"read":12,"write":3}}}}
EOF`

func TestSevenCodeIsRegistered(t *testing.T) {
	d, ok := Describe("sevencode")
	if !ok {
		t.Fatal("sevencode is not registered")
	}
	if !d.NeedsCredential() || len(d.Credentials) != 2 {
		t.Fatalf("credentials: %+v", d.Credentials)
	}
	api, _ := d.Credential(CredAPIKey)
	if api.EnvVar != "SEVENCODE_API_KEY" || api.Path != "" {
		t.Fatalf("the token is brokered as the variable the provider entry references: %+v", api)
	}
	// The CLI writes its login into its DATA directory, not into a directory of
	// its own name — the first draft guessed `.sevencode/credentials.json`, and
	// a credential written where nothing reads it is a credential that is not
	// there.
	login, _ := d.Credential(CredSubscription)
	if login.Path != ".local/share/opencode/auth.json" || login.EnvVar != "" {
		t.Fatalf("the login is a file in the CLI's data directory: %+v", login)
	}
	if !safeCredentialPath(login.Path) {
		t.Fatalf("the declared login path has to stay inside the home: %q", login.Path)
	}
	// Measured, not assumed: the session id stands on every event and
	// `run --session` continues the session with its history. An engine that
	// resumes may carry an agent that waits for an answer (spec/03).
	if !d.Capabilities.Resume {
		t.Error("the session id was measured — the engine resumes")
	}
	if d.Capabilities.SkillsDir != ".opencode/skill" {
		t.Errorf("skills dir = %q, measured is .opencode/skill", d.Capabilities.SkillsDir)
	}
	// What is the PROVIDER's remains undeclared: the variant names belong to
	// the instance behind the gateway, the model list too.
	if len(d.Capabilities.EffortLevels) != 0 || len(d.Capabilities.Models) != 0 {
		t.Fatalf("what belongs to the provider must stay undeclared: %+v", d.Capabilities)
	}
	if d.DefaultModel() != "" || !d.AcceptsModel("sevenai-qwen") {
		t.Fatal("in front of one gateway the model list is the instance's, not ours")
	}
}

func TestSevenCodeRunsTheMeasuredSurface(t *testing.T) {
	bin, dir := fakeSevenCode(t, sevencodeStream)
	adapter := &SevenCode{Binary: bin}

	events := 0
	res, err := adapter.Run(context.Background(), RunSpec{
		TaskID: "t1", Title: "Ticket 42", Body: "Bitte prüfen",
		SystemPrompt:  "Du bist der Support-Agent.",
		MemoryContext: "Letzter Stand: Rückfrage offen.",
		HomeDir:       dir,
	}, func(string, json.RawMessage) { events++ })
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "done" || res.Result != "Ticket beantwortet" || res.Memory != "Kunde nutzt Firefox" {
		t.Fatalf("result wrong: %+v", res)
	}
	// Three lines, three recording events: what the adapter does not act on is
	// still what somebody reads afterwards.
	if events != 3 {
		t.Fatalf("every event line belongs in the recording, got %d", events)
	}
	if res.SessionID != "ses_abc" {
		t.Fatalf("the session id stands on every event: %+v", res)
	}
	if !res.Measured() || res.InputTokens != 42 || res.OutputTokens != 7 ||
		res.CacheReadTokens != 12 || res.CacheCreationTokens != 3 || res.CostUSD != 0.0125 {
		t.Fatalf("the figures of step_finish belong to the run: %+v", res)
	}

	args := readFile(t, filepath.Join(dir, "args.txt"))
	if !strings.Contains(args, "run") || !strings.Contains(args, "--format") || !strings.Contains(args, "json") {
		t.Fatalf("the headless run is `run --format json`:\n%s", args)
	}
	// The flags of the first draft, which the binary does not have. `-p` is the
	// worst of them: in `run` it means --password.
	for _, gone := range []string{"-p", "--auto", "--json", "--resume", "--yolo"} {
		for _, line := range strings.Split(args, "\n") {
			if line == gone {
				t.Fatalf("%s does not exist in this CLI:\n%s", gone, args)
			}
		}
	}
	if strings.Contains(args, "--model") {
		t.Fatal("without a configured model the CLI's own default applies")
	}
	// The message is positional and goes last, so nothing after it can be read
	// as an option — everything past the flags is the one argument (it carries
	// newlines of its own, which is why this is a suffix and not a line).
	message := lastArg(t, args, "json")
	for _, want := range []string{"Du bist der Support-Agent.", "Letzter Stand: Rückfrage offen.", "Ticket 42"} {
		if !strings.Contains(message, want) {
			t.Fatalf("prompt part %q is not in the message:\n%s", want, args)
		}
	}
	if strings.Index(message, "Du bist der Support-Agent.") > strings.Index(message, "Ticket 42") {
		t.Fatal("the compiled config goes in front of the task, not behind it")
	}
}

func TestSevenCodeModelAndEffortPassThrough(t *testing.T) {
	bin, dir := fakeSevenCode(t, sevencodeStream)
	if _, err := (&SevenCode{Binary: bin}).Run(context.Background(),
		RunSpec{Model: "sevenai-qwen", Effort: "high", HomeDir: dir},
		func(string, json.RawMessage) {}); err != nil {
		t.Fatal(err)
	}
	args := readFile(t, filepath.Join(dir, "args.txt"))
	if !strings.Contains(args, "--model") || !strings.Contains(args, "sevenai-qwen") {
		t.Fatalf("a configured model is the CLI's own flag to take:\n%s", args)
	}
	// The reasoning lever is called --variant here, and its values are the
	// provider's — covey passes on what was entered rather than a list of its
	// own.
	if !strings.Contains(args, "--variant") || !strings.Contains(args, "high") {
		t.Fatalf("the effort goes in as --variant:\n%s", args)
	}
}

// A resumed run speaks into the session the CLI kept. Measured: the double saw
// the earlier turns come back with the second run, so the brief must not be
// sent a second time.
func TestSevenCodeResumesTheSession(t *testing.T) {
	bin, dir := fakeSevenCode(t, sevencodeStream)
	res, err := (&SevenCode{Binary: bin}).Run(context.Background(), RunSpec{
		Title: "Ticket 42", Body: "Bitte prüfen", SystemPrompt: "Du bist der Support-Agent.",
		ResumeSessionID: "ses_abc", ResumeInput: "Das Ereignis ist eingetreten.",
		HomeDir: dir,
	}, func(string, json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "done" {
		t.Fatalf("a resumed run finishes like any other: %+v", res)
	}
	args := readFile(t, filepath.Join(dir, "args.txt"))
	if !strings.Contains(args, "--session") || !strings.Contains(args, "ses_abc") {
		t.Fatalf("the session has to be named:\n%s", args)
	}
	if msg := lastArg(t, args, "ses_abc"); msg != "Das Ereignis ist eingetreten." {
		t.Fatalf("a resume sends the new input, not the whole brief again: %q", msg)
	}
}

// A session error is the CLI's own sentence about a run that did not happen —
// no credential, no model, an endpoint that refused. It beats the exit code,
// which carries a number.
func TestSevenCodeSessionErrorBeatsTheExitCode(t *testing.T) {
	bin, dir := fakeSevenCode(t, `cat <<'EOF'
{"type":"error","timestamp":1,"sessionID":"ses_x","error":{"name":"ProviderAuthError","data":{"message":"no providers found"}}}
EOF
exit 1`)
	res, err := (&SevenCode{Binary: bin}).Run(context.Background(),
		RunSpec{Title: "T", Body: "B", HomeDir: dir}, func(string, json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "failed" || !strings.Contains(res.Error, "no providers found") {
		t.Fatalf("the CLI's own sentence belongs in the task: %+v", res)
	}
}

func TestSevenCodeFailureCarriesTheReason(t *testing.T) {
	bin, dir := fakeSevenCode(t, "echo 'no credential configured' >&2\nexit 3")
	res, err := (&SevenCode{Binary: bin}).Run(context.Background(),
		RunSpec{Title: "T", Body: "B", HomeDir: dir}, func(string, json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "failed" {
		t.Fatalf("exit 3 is a failure: %+v", res)
	}
	if !strings.Contains(res.Error, "no credential configured") {
		t.Fatalf("stderr lost: %+v", res)
	}
}

// TestSevenCodeEnvironment checks what the run is given: the permission setting
// without which the CLI rejects its own tools, the brokered token undisturbed,
// and none of the daemon's own variables.
func TestSevenCodeEnvironment(t *testing.T) {
	bin, dir := fakeSevenCode(t, sevencodeStream)
	t.Setenv("COVEY_DAEMON_TOKEN", "daemon-geheim")

	_, err := (&SevenCode{Binary: bin}).Run(
		context.Background(),
		RunSpec{HomeDir: dir, Env: []string{"SEVENCODE_API_KEY=brokered-token", "COVEY_ACTION_PORT=8081"}},
		func(string, json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	env := readFile(t, filepath.Join(dir, "env.txt"))
	if !strings.Contains(env, "SEVENCODE_API_KEY=brokered-token") {
		t.Fatalf("the brokered credential has to reach the run:\n%s", env)
	}
	// Without this the CLI answers its own permission request with a rejection,
	// and an agent that may not use bash looks like an agent that cannot work.
	if !strings.Contains(env, `OPENCODE_PERMISSION={"bash":"allow"`) {
		t.Fatalf("the run has to be allowed its tools:\n%s", env)
	}
	if strings.Contains(env, "daemon-geheim") {
		t.Fatal("the daemon's own control-plane credentials stay out of the run")
	}
	if !strings.Contains(env, "COVEY_ACTION_PORT=8081") {
		t.Fatal("what the caller hands the run explicitly still belongs to it")
	}
	if !strings.Contains(env, "HOME="+dir) {
		t.Fatalf("HOME has to point at the agent home:\n%s", env)
	}
}

func TestSevenCodeBinaryStaysOverridable(t *testing.T) {
	t.Setenv("COVEY_SEVENCODE_BIN", "/opt/seven/sevencode")
	if got := NewSevenCode(); got.Binary != "/opt/seven/sevencode" {
		t.Fatal("the binary stays overridable, as with the other engines")
	}
}

// lastArg returns the positional message: everything the fake CLI wrote after
// the last flag value. The message carries newlines, so it cannot be read as a
// line of the argument dump.
func lastArg(t *testing.T, args, afterFlag string) string {
	t.Helper()
	marker := afterFlag + "\n"
	i := strings.LastIndex(args, marker)
	if i < 0 {
		t.Fatalf("flag value %q not in the arguments:\n%s", afterFlag, args)
	}
	return strings.TrimRight(args[i+len(marker):], "\n")
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
