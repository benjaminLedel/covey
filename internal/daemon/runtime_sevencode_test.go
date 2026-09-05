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
		t.Fatalf("the API token is the variable the CLI names for CI: %+v", api)
	}
	login, _ := d.Credential(CredSubscription)
	if login.Path != ".sevencode/credentials.json" || login.EnvVar != "" {
		t.Fatalf("`sevencode login` writes a file into the home: %+v", login)
	}
	if !safeCredentialPath(login.Path) {
		t.Fatalf("the declared login path has to stay inside the home: %q", login.Path)
	}
	// Both are the state of knowledge, not a limitation of the engine: nothing
	// documented prints the id of the session a run had, so nothing can be
	// resumed, and nothing documented names an effort level.
	if d.Capabilities.Resume || d.Capabilities.SkillsDir != "" ||
		len(d.Capabilities.EffortLevels) != 0 || len(d.Capabilities.Models) != 0 {
		t.Fatalf("unverified capabilities must stay undeclared: %+v", d.Capabilities)
	}
	if d.DefaultModel() != "" || !d.AcceptsModel("sevenai-qwen") {
		t.Fatal("in front of one gateway the model list is the instance's, not ours")
	}
}

func TestSevenCodeFlagsAndResult(t *testing.T) {
	bin, dir := fakeSevenCode(t, `cat <<'EOF'
Erledigt.
COVEY_STATUS: {"status":"done","result":"Ticket beantwortet","memory":"Kunde nutzt Firefox"}
EOF`)
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
	// No event schema means no events, and a run without any measured figure
	// sends no cost message: the honest record of a run nobody priced.
	if events != 0 {
		t.Fatalf("without a verified event schema nothing may be claimed: %d", events)
	}
	if res.SessionID != "" || res.Measured() {
		t.Fatalf("no session id and no usage on this path: %+v", res)
	}

	args := readFile(t, filepath.Join(dir, "args.txt"))
	if !strings.Contains(args, "-p") || !strings.Contains(args, "--auto") {
		t.Fatalf("one-shot without permission prompts is the documented surface:\n%s", args)
	}
	if strings.Contains(args, "--yolo") {
		t.Fatal("--yolo drops the CLI's own check, --auto keeps it")
	}
	if strings.Contains(args, "--no-config") {
		t.Fatal("a project's own configuration belongs to the work")
	}
	if strings.Contains(args, "--model") {
		t.Fatal("without a configured model the CLI's own default applies")
	}
	// The system prompt has to travel inside the single argument: the CLI names
	// no flag for it.
	for _, want := range []string{"Du bist der Support-Agent.", "Letzter Stand: Rückfrage offen.",
		"Ticket 42", "Bitte prüfen"} {
		if !strings.Contains(args, want) {
			t.Fatalf("prompt part %q missing from the argument:\n%s", want, args)
		}
	}
	if strings.Index(args, "Du bist der Support-Agent.") > strings.Index(args, "Ticket 42") {
		t.Fatal("the compiled config goes in front of the task, not behind it")
	}
}

func TestSevenCodeModelPassesThrough(t *testing.T) {
	bin, _ := fakeSevenCode(t, "echo fertig")
	if _, err := (&SevenCode{Binary: bin}).Run(context.Background(),
		RunSpec{Model: "sevenai-qwen", HomeDir: t.TempDir()}, func(string, json.RawMessage) {}); err != nil {
		t.Fatal(err)
	}
	args := readFile(t, filepath.Join(filepath.Dir(bin), "args.txt"))
	if !strings.Contains(args, "--model") || !strings.Contains(args, "sevenai-qwen") {
		t.Fatalf("a configured model is the CLI's own flag to take:\n%s", args)
	}
}

// TestSevenCodeRefusesResume keeps the declaration and the run in step: an
// engine that declares no Resume must not quietly answer a resume request with a
// fresh run that lost the conversation.
func TestSevenCodeRefusesResume(t *testing.T) {
	bin, dir := fakeSevenCode(t, "echo fertig")
	res, err := (&SevenCode{Binary: bin}).Run(context.Background(), RunSpec{
		ResumeSessionID: "sess-1", ResumeInput: "Das Ereignis ist eingetreten.",
		HomeDir: dir,
	}, func(string, json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "failed" || !strings.Contains(res.Error, "spec/25") {
		t.Fatalf("a resume has to fail loudly and name where the gap is: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "args.txt")); err == nil {
		t.Fatal("the CLI must not even be started for a run that cannot continue")
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
	// Without an event stream the exit code is the only failure signal, so the
	// CLI's own words belong in the task's error text rather than in the daemon's.
	if !strings.Contains(res.Error, "no credential configured") {
		t.Fatalf("stderr lost: %+v", res)
	}
}

// TestSevenCodeEnvironment checks the two ordering claims of Run(): the
// endpoint goes in without burying the brokered token, and the daemon's own
// COVEY_* variables stay out of the child.
func TestSevenCodeEnvironment(t *testing.T) {
	bin, dir := fakeSevenCode(t, "echo fertig")
	t.Setenv("COVEY_DAEMON_TOKEN", "daemon-geheim")
	t.Setenv("COVEY_SEVENCODE_BASE_URL", "https://educa.example.internal/")

	_, err := (&SevenCode{Binary: bin, BaseURL: "https://educa.example.internal"}).Run(
		context.Background(),
		RunSpec{HomeDir: dir, Env: []string{"SEVENCODE_API_KEY=brokered-token", "COVEY_ACTION_PORT=8081"}},
		func(string, json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	env := readFile(t, filepath.Join(dir, "env.txt"))
	if !strings.Contains(env, "SEVENCODE_API_KEY=brokered-token") {
		t.Fatalf("the endpoint must not override the brokered credential:\n%s", env)
	}
	if !strings.Contains(env, "SEVENCODE_API_BASE=https://educa.example.internal") {
		t.Fatal("the configured endpoint has to reach the CLI")
	}
	if strings.Contains(env, "daemon-geheim") {
		t.Fatal("the daemon's own control-plane credentials stay out of the run")
	}
	if strings.Contains(env, "COVEY_SEVENCODE_BASE_URL=") {
		t.Fatal("the daemon's COVEY_* variables are stripped, the endpoint reaches the CLI as SEVENCODE_API_BASE")
	}
	if !strings.Contains(env, "COVEY_ACTION_PORT=8081") {
		t.Fatal("what the caller hands the run explicitly still belongs to it")
	}
	if !strings.Contains(env, "HOME="+dir) {
		t.Fatalf("HOME has to point at the agent home:\n%s", env)
	}
}

func TestSevenCodeEndpointDefaultsToNothing(t *testing.T) {
	t.Setenv("COVEY_SEVENCODE_BASE_URL", "")
	if got := NewSevenCode(); got.BaseURL != "" {
		t.Fatalf("no default endpoint is guessed for this CLI: %q", got.BaseURL)
	}
	t.Setenv("COVEY_SEVENCODE_BIN", "/opt/seven/sevencode")
	if got := NewSevenCode(); got.Binary != "/opt/seven/sevencode" {
		t.Fatal("the binary stays overridable, as with the other engines")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
