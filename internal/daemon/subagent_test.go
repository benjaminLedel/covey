package daemon

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"covey/internal/target"
)

// TestGitChangesSince secures the core of the report: it is measured against the
// checkout's baseline COMMIT, not against a `git status` snapshot taken earlier.
// The sub-agent may commit locally in the checkout — many projects demand it in
// their CLAUDE.md — and precisely then `git status` shows nothing any more.
// Without this test half the work would silently drop out of the list that goes
// to the commit action.
func TestGitChangesSince(t *testing.T) {
	dir := gitRepo(t)

	// Baseline as after a checkout: upstream state committed and tagged.
	writeFile(t, dir, "app.go", "package app")
	writeFile(t, dir, "alt.go", "package app")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "covey baseline")
	runGit(t, dir, "tag", target.BaselineRef)

	base := gitRev(t.Context(), dir, target.BaselineRef)
	if base == "" {
		t.Fatal("the baseline tag must be resolvable")
	}
	if changed, deleted := gitChangesSince(t.Context(), dir, base); len(changed)+len(deleted) != 0 {
		t.Fatalf("a fresh baseline = no work, was: %v / %v", changed, deleted)
	}

	// The sub-agent commits part of its work locally …
	writeFile(t, dir, "app.go", "package app // fix")
	writeFile(t, dir, "committet.go", "package app")
	runGit(t, dir, "rm", "-q", "alt.go")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "fix")
	// … and leaves the rest lying open in the working tree.
	writeFile(t, dir, "offen.go", "package app")

	changed, deleted := gitChangesSince(t.Context(), dir, base)
	wantChanged := []string{"app.go", "committet.go", "offen.go"}
	if !reflect.DeepEqual(changed, wantChanged) {
		t.Fatalf("changed wrong: %v (expected %v)", changed, wantChanged)
	}
	if !reflect.DeepEqual(deleted, []string{"alt.go"}) {
		t.Fatalf("deleted wrong: %v (expected [alt.go])", deleted)
	}
}

// What gets reported is the ENTIRE state against the baseline, not only what
// this one sub-run touched. That is intentional: the lists go unchanged into the
// commit action, and whatever an earlier sub-run of the same task changed
// belongs into the merge request just as much — otherwise it would stay on disk
// and never arrive in the target system.
func TestGitChangesSinceIsCumulative(t *testing.T) {
	dir := gitRepo(t)
	writeFile(t, dir, "app.go", "package app")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "covey baseline")
	runGit(t, dir, "tag", target.BaselineRef)
	base := gitRev(t.Context(), dir, target.BaselineRef)

	writeFile(t, dir, "frueher.go", "package app") // earlier run, not yet committed
	writeFile(t, dir, "app.go", "package app // jetzt")

	changed, _ := gitChangesSince(t.Context(), dir, base)
	if !reflect.DeepEqual(changed, []string{"app.go", "frueher.go"}) {
		t.Fatalf("changed wrong: %v (expected [app.go frueher.go])", changed)
	}
}

// Umlauts, spaces and quotes in paths must arrive RAW in the report: the lists
// go unchanged into the commit action. git escapes paths outside ASCII by
// default (core.quotepath=true, which is how it is in the sandbox) — prüfung.go
// would become "pr\303\274fung.go", and exactly that mangled path would land in
// the target system.
func TestGitChangesSinceRawPaths(t *testing.T) {
	dir := gitRepo(t)
	// Pin the sandbox's default locally so the test reproduces independently of
	// the developer machine's global configuration.
	runGit(t, dir, "config", "core.quotepath", "true")

	writeFile(t, dir, "alt ümlaut.go", "package app")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "covey baseline")
	runGit(t, dir, "tag", target.BaselineRef)
	base := gitRev(t.Context(), dir, target.BaselineRef)

	// Committed: a rename, umlaut and space on both sides.
	runGit(t, dir, "mv", "alt ümlaut.go", "neu geprüft.go")
	runGit(t, dir, "commit", "-q", "-m", "umbenannt")
	// Open in the working tree: a new file, likewise with an umlaut.
	writeFile(t, dir, "änderung offen.txt", "x")

	changed, deleted := gitChangesSince(t.Context(), dir, base)
	if want := []string{"neu geprüft.go", "änderung offen.txt"}; !reflect.DeepEqual(changed, want) {
		t.Fatalf("changed must name the raw paths: %q (expected %q)", changed, want)
	}
	if want := []string{"alt ümlaut.go"}; !reflect.DeepEqual(deleted, want) {
		t.Fatalf("the source of the rename must come along raw: %q (expected %q)", deleted, want)
	}
}

// Without a repository there is no anchor. The sub-run then reports no file list
// rather than a wrong one — but it must not fail because of it.
func TestGitChangesSinceWithoutRepo(t *testing.T) {
	dir := t.TempDir()
	if got := gitRev(t.Context(), dir, "HEAD"); got != "" {
		t.Fatalf("without a repository there is no anchor: %q", got)
	}
	changed, deleted := gitChangesSince(t.Context(), dir, "")
	if len(changed)+len(deleted) != 0 {
		t.Fatalf("without an anchor nothing may be reported: %v / %v", changed, deleted)
	}
}

// Marking a sub-run event must not hide the runtime's format: recording and
// timeline read stream-json directly and would otherwise show the sub-run as a
// JSON blob instead of a turn with its tool calls.
// subAgentMarkOf reads the mark out of a marked line.
func subAgentMarkOf(t *testing.T, line json.RawMessage) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(line, &obj); err != nil {
		t.Fatalf("the marked line is not JSON: %s", line)
	}
	mark, ok := obj["covey_sub_agent"].(map[string]any)
	if !ok {
		t.Fatalf("sub-run mark missing: %s", line)
	}
	return mark
}

func TestMarkSubAgentKeepsStreamFormat(t *testing.T) {
	got, stamped := markSubAgent(json.RawMessage(`{"type":"assistant","message":{"content":[]}}`), "/home/agent/repos/p", "7", "")
	if !stamped {
		t.Fatal("a JSON object must be marked")
	}

	var obj map[string]any
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatal(err)
	}
	if obj["type"] != "assistant" {
		t.Fatalf("type must stay at the top: %s", got)
	}
	mark := subAgentMarkOf(t, got)
	if mark["dir"] != "/home/agent/repos/p" || mark["run"] != "7" {
		t.Fatalf("mark incomplete: %s", got)
	}
	if _, ok := mark["task"]; ok {
		t.Fatalf("without a work order no task key may appear: %s", got)
	}
}

// Lines that are not JSON objects are deliberately passed through by the
// adapter. They must neither be altered nor reported as marked — otherwise the
// work order would count as placed although nobody carries it.
func TestMarkSubAgentPassesNonObjects(t *testing.T) {
	for _, line := range []string{"kein json", `["liste"]`, `null`} {
		got, stamped := markSubAgent(json.RawMessage(line), "/repo", "1", "Auftrag")
		if stamped || string(got) != line {
			t.Fatalf("%q must pass through unchanged and unmarked, was %q (stamped=%v)", line, got, stamped)
		}
	}
}

// The work order is the sub-run's heading in the timeline. That is why it sits
// in the mark — shortened, because there it is only meant to title.
func TestMarkSubAgentCarriesTask(t *testing.T) {
	got, _ := markSubAgent(json.RawMessage(`{"type":"system","subtype":"init"}`), "/repo", "1", "  Nullpointer beheben  ")
	if task := subAgentMarkOf(t, got)["task"]; task != "Nullpointer beheben" {
		t.Fatalf("the work order must appear trimmed in the mark: %s", got)
	}

	// Long orders are shortened rune-safely — no half character, no timeline
	// choking on the order text.
	got, _ = markSubAgent(json.RawMessage(`{"type":"system"}`), "/repo", "1", strings.Repeat("ä", maxMarkedTask+50))
	task, _ := subAgentMarkOf(t, got)["task"].(string)
	if r := []rune(task); len(r) != maxMarkedTask+1 || r[len(r)-1] != '…' {
		t.Fatalf("the work order must be shortened to %d runes plus an ellipsis, was %d", maxMarkedTask, len([]rune(task)))
	}
}

// The core of the marking: dir and run stand on EVERY line of a run — that is
// how the timeline recognizes its lines, even when a second sub-run cuts in. The
// work order, by contrast, appears exactly once, otherwise it would enter the
// recording with its length × the number of lines.
func TestSubAgentStamperGivesTaskOnce(t *testing.T) {
	stamp := subAgentStamper("/repo", "3", "Nullpointer beheben")

	// A non-JSON line first: it must not use up the work order.
	if got := stamp(json.RawMessage("Warnung aus dem Wrapper")); string(got) != "Warnung aus dem Wrapper" {
		t.Fatalf("non-JSON must pass through unchanged: %s", got)
	}

	var withTask int
	for i := range 3 {
		mark := subAgentMarkOf(t, stamp(json.RawMessage(`{"type":"assistant"}`)))
		if mark["dir"] != "/repo" || mark["run"] != "3" {
			t.Fatalf("line %d without a complete run identifier: %v", i, mark)
		}
		if _, ok := mark["task"]; ok {
			withTask++
		}
	}
	if withTask != 1 {
		t.Fatalf("exactly one line carries the work order, there were %d", withTask)
	}
}

// Two sub-runs of the same daemon must get distinguishable identifiers —
// otherwise they merge into one block in the timeline.
func TestSubAgentRunsAreDistinct(t *testing.T) {
	bin, home := fakeClaude(t, `cat <<'EOF'
{"type":"result","subtype":"success","session_id":"s","result":"fertig"}
EOF`)
	c := &Client{homeDir: home, runtimes: map[string]Runtime{"claude-code": &ClaudeCode{Binary: bin}},
		creds: map[string]InjectCredentials{}, cfg: InjectConfig{Runtime: "claude-code"}}

	req := target.SubAgentRequest{Dir: home, Task: "Fix"}
	if _, err := c.runSubAgent(t.Context(), "task-1", req); err != nil {
		t.Fatal(err)
	}
	first := c.subRuns.Load()
	if _, err := c.runSubAgent(t.Context(), "task-1", req); err != nil {
		t.Fatal(err)
	}
	if c.subRuns.Load() == first {
		t.Fatal("every sub-run needs an identifier of its own")
	}
}

// gitRepo creates an empty repository and skips the test if no git is available
// (like TestCheckoutGitBaseline in the gitlab package).
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := exec.Command("git", "-C", dir, "init", "-q").Run(); err != nil {
		t.Skipf("no git available: %v", err)
	}
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	// Identity as ENV instead of via git config — like initGitBaseline in the
	// checkout.
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Covey", "GIT_AUTHOR_EMAIL=covey@localhost",
		"GIT_COMMITTER_NAME=Covey", "GIT_COMMITTER_EMAIL=covey@localhost")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// At the turn limit the adapter reports status "incomplete" plus the handover
// state instead of a result. The sub-run must pass that on as turns_exhausted —
// otherwise the commissioning agent takes half-finished work for finished.
func TestSubAgentReportsTurnLimit(t *testing.T) {
	// The first run ends at the turn limit; the adapter then fetches the
	// handover state via --resume from the same session.
	bin, home := fakeClaude(t, `
if printf '%s\n' "$@" | grep -q -- '--resume'; then
cat <<'EOF'
{"type":"result","subtype":"success","session_id":"s","result":"## Erledigt\nHälfte des Fixes"}
EOF
else
cat <<'EOF'
{"type":"result","subtype":"error_max_turns","session_id":"s","num_turns":60,"total_cost_usd":0.5}
EOF
fi`)
	c := &Client{homeDir: home, runtimes: map[string]Runtime{"claude-code": &ClaudeCode{Binary: bin}},
		creds: map[string]InjectCredentials{}, cfg: InjectConfig{Runtime: "claude-code"}}

	res, err := c.runSubAgent(t.Context(), "task-1", target.SubAgentRequest{Dir: home, Task: "Fix den Bug"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TurnsExhausted {
		t.Fatalf("the turn limit must arrive as turns_exhausted: %+v", res)
	}
	if !strings.Contains(res.Result, "Erledigt") {
		t.Fatalf("the handover state must appear in the report: %+v", res)
	}
}

// The sub-run is hermetic: no COVEY_ACTION_PORT, hence no route to target
// systems — and none through the back door of the daemon environment either. It
// contains COVEY_WS_URL and COVEY_DAEMON_TOKEN; with those a hook or MCP server
// from the repo could open its own WebSocket to the control plane and send
// `request_credential` itself, reaching exactly the brokered accesses that the
// missing action proxy keeps away. The brokered LLM key, by contrast, must
// arrive, otherwise the runtime does not run.
func TestSubAgentEnvIsHermetic(t *testing.T) {
	t.Setenv("COVEY_DAEMON_TOKEN", "daemon-jwt-geheim")
	t.Setenv("COVEY_WS_URL", "wss://covey.example/api/daemon/ws")
	bin, home := fakeClaude(t, `
printf 'port=%s key=%s token=%s ws=%s\n' "$COVEY_ACTION_PORT" "$ANTHROPIC_API_KEY" \
  "$COVEY_DAEMON_TOKEN" "$COVEY_WS_URL" > "$HOME/env.txt"
cat <<'EOF'
{"type":"result","subtype":"success","session_id":"s","result":"fertig"}
EOF`)
	c := &Client{homeDir: home, runtimes: map[string]Runtime{"claude-code": &ClaudeCode{Binary: bin}},
		creds: map[string]InjectCredentials{
			"anthropic": {Granted: true, Token: "sk-ant-api-geheim", EnvVar: "ANTHROPIC_API_KEY"},
		},
		cfg: InjectConfig{Runtime: "claude-code"}}

	if _, err := c.runSubAgent(t.Context(), "task-1", target.SubAgentRequest{Dir: home, Task: "x"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(home, "env.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), "port= ") {
		t.Fatalf("the sub-run must not see an action proxy: %q", got)
	}
	if !strings.Contains(string(got), "sk-ant-api-geheim") {
		t.Fatalf("brokered LLM key missing: %q", got)
	}
	if !strings.Contains(string(got), "token= ws=") {
		t.Fatalf("the daemon's credentials must not be inherited into the sub-run: %q", got)
	}
}

// One sub-run per checkout. Two parallel runs in the same directory overwrote
// each other's files and both reported the same cumulative state — the
// commissioning agent would get two reports about one blended piece of work.
// Different directories remain possible in parallel.
func TestSubAgentOnePerDir(t *testing.T) {
	bin, home := fakeClaude(t, `
cat <<'EOF'
{"type":"result","subtype":"success","session_id":"s","result":"fertig"}
EOF`)
	c := &Client{homeDir: home, runtimes: map[string]Runtime{"claude-code": &ClaudeCode{Binary: bin}},
		creds: map[string]InjectCredentials{}, cfg: InjectConfig{Runtime: "claude-code"}}
	other := filepath.Join(home, "zweites-projekt")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}

	// Occupied, as a still running sub-agent would leave it.
	if err := c.claimSubAgentDir(home); err != nil {
		t.Fatal(err)
	}
	if _, err := c.runSubAgent(t.Context(), "task-1", target.SubAgentRequest{Dir: home, Task: "x"}); err == nil {
		t.Fatal("a second order in the same checkout must be rejected")
	}
	if _, err := c.runSubAgent(t.Context(), "task-1", target.SubAgentRequest{Dir: other, Task: "x"}); err != nil {
		t.Fatalf("another checkout is not affected by it: %v", err)
	}
	// After the report the directory is free again.
	c.releaseSubAgentDir(home)
	if _, err := c.runSubAgent(t.Context(), "task-1", target.SubAgentRequest{Dir: home, Task: "x"}); err != nil {
		t.Fatalf("after completion the same checkout must work again: %v", err)
	}
}

// A cwd that does not exist fails with a message in the style of the other
// validations — not only at the subprocess's chdir ("starting claude: chdir …"),
// which tells the agent nothing about its mistake.
func TestSubAgentRejectsMissingDir(t *testing.T) {
	bin, home := fakeClaude(t, `
cat <<'EOF'
{"type":"result","subtype":"success","session_id":"s","result":"fertig"}
EOF`)
	c := &Client{homeDir: home, runtimes: map[string]Runtime{"claude-code": &ClaudeCode{Binary: bin}},
		creds: map[string]InjectCredentials{}, cfg: InjectConfig{Runtime: "claude-code"}}

	_, err := c.runSubAgent(t.Context(), "task-1",
		target.SubAgentRequest{Dir: "repos/gibts-nicht", Task: "x"})
	if err == nil || !strings.Contains(err.Error(), "checkout result") {
		t.Fatalf("a missing cwd must be rejected up front, was: %v", err)
	}
}
