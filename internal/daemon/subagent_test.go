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

// TestGitChangesSince sichert den Kern des Berichts: Gemessen wird gegen den
// Baseline-COMMIT des Checkouts, nicht gegen ein `git status`-Abbild von
// vorher. Der Sub-Agent darf im Checkout lokal committen — viele Projekte
// verlangen das in ihrer CLAUDE.md —, und genau dann sieht `git status`
// nichts mehr. Ohne diesen Test fiele die halbe Arbeit lautlos aus der Liste,
// die an die commit-Aktion geht.
func TestGitChangesSince(t *testing.T) {
	dir := gitRepo(t)

	// Baseline wie nach einem Checkout: Upstream-Stand committet und getaggt.
	writeFile(t, dir, "app.go", "package app")
	writeFile(t, dir, "alt.go", "package app")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "covey baseline")
	runGit(t, dir, "tag", target.BaselineRef)

	base := gitRev(t.Context(), dir, target.BaselineRef)
	if base == "" {
		t.Fatal("Baseline-Tag muss auflösbar sein")
	}
	if changed, deleted := gitChangesSince(t.Context(), dir, base); len(changed)+len(deleted) != 0 {
		t.Fatalf("frische Baseline = keine Arbeit, war: %v / %v", changed, deleted)
	}

	// Der Sub-Agent committet einen Teil seiner Arbeit lokal …
	writeFile(t, dir, "app.go", "package app // fix")
	writeFile(t, dir, "committet.go", "package app")
	runGit(t, dir, "rm", "-q", "alt.go")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "fix")
	// … und lässt den Rest offen im Arbeitsverzeichnis liegen.
	writeFile(t, dir, "offen.go", "package app")

	changed, deleted := gitChangesSince(t.Context(), dir, base)
	wantChanged := []string{"app.go", "committet.go", "offen.go"}
	if !reflect.DeepEqual(changed, wantChanged) {
		t.Fatalf("changed falsch: %v (erwartet %v)", changed, wantChanged)
	}
	if !reflect.DeepEqual(deleted, []string{"alt.go"}) {
		t.Fatalf("deleted falsch: %v (erwartet [alt.go])", deleted)
	}
}

// Gemeldet wird der GESAMTE Stand gegen die Baseline, nicht nur das, was
// dieser eine Sub-Lauf angefasst hat. Das ist Absicht: Die Listen gehen
// unverändert in die commit-Aktion, und was ein früherer Sub-Lauf derselben
// Aufgabe geändert hat, gehört genauso in den Merge Request — sonst bliebe es
// auf Platte liegen und käme nie im Zielsystem an.
func TestGitChangesSinceIsCumulative(t *testing.T) {
	dir := gitRepo(t)
	writeFile(t, dir, "app.go", "package app")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "covey baseline")
	runGit(t, dir, "tag", target.BaselineRef)
	base := gitRev(t.Context(), dir, target.BaselineRef)

	writeFile(t, dir, "frueher.go", "package app") // Vorlauf, noch nicht committet
	writeFile(t, dir, "app.go", "package app // jetzt")

	changed, _ := gitChangesSince(t.Context(), dir, base)
	if !reflect.DeepEqual(changed, []string{"app.go", "frueher.go"}) {
		t.Fatalf("changed falsch: %v (erwartet [app.go frueher.go])", changed)
	}
}

// Ohne Repository gibt es keinen Anker. Dann meldet der Sub-Lauf lieber keine
// Dateiliste, als eine falsche — scheitern darf er deswegen nicht.
func TestGitChangesSinceWithoutRepo(t *testing.T) {
	dir := t.TempDir()
	if got := gitRev(t.Context(), dir, "HEAD"); got != "" {
		t.Fatalf("ohne Repository gibt es keinen Anker: %q", got)
	}
	changed, deleted := gitChangesSince(t.Context(), dir, "")
	if len(changed)+len(deleted) != 0 {
		t.Fatalf("ohne Anker darf nichts gemeldet werden: %v / %v", changed, deleted)
	}
}

// Die Markierung eines Sub-Lauf-Events darf das Format der Runtime nicht
// verdecken: Aufzeichnung und Timeline lesen stream-json direkt und würden den
// Sub-Lauf sonst als JSON-Klumpen statt als Turn mit Tool-Aufrufen zeigen.
func TestMarkSubAgentKeepsStreamFormat(t *testing.T) {
	got := markSubAgent(json.RawMessage(`{"type":"assistant","message":{"content":[]}}`), "/home/agent/repos/p")

	var obj map[string]any
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatal(err)
	}
	if obj["type"] != "assistant" {
		t.Fatalf("type muss oben stehen bleiben: %s", got)
	}
	mark, ok := obj["covey_sub_agent"].(map[string]any)
	if !ok || mark["dir"] != "/home/agent/repos/p" {
		t.Fatalf("Sub-Lauf-Markierung fehlt oder ist unvollständig: %s", got)
	}
}

// gitRepo legt ein leeres Repository an und überspringt den Test, wenn kein
// git verfügbar ist (wie TestCheckoutGitBaseline im gitlab-Paket).
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := exec.Command("git", "-C", dir, "init", "-q").Run(); err != nil {
		t.Skipf("kein git verfügbar: %v", err)
	}
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	// Identität als ENV statt via git config — wie initGitBaseline im Checkout.
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

// Am Turn-Limit meldet der Adapter Status "incomplete" plus den Übergabe-Stand
// statt eines Ergebnisses. Der Sub-Lauf muss das als turns_exhausted
// durchreichen — sonst hält der beauftragende Agent die halbe Arbeit für fertig.
func TestSubAgentReportsTurnLimit(t *testing.T) {
	// Erster Lauf endet am Turn-Limit; den Übergabe-Stand holt der Adapter
	// danach per --resume aus derselben Session.
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
		t.Fatalf("Turn-Limit muss als turns_exhausted ankommen: %+v", res)
	}
	if !strings.Contains(res.Result, "Erledigt") {
		t.Fatalf("Übergabe-Stand muss im Bericht stehen: %+v", res)
	}
}

// Der Sub-Lauf ist hermetisch: kein COVEY_ACTION_PORT, also kein Weg zu
// Zielsystemen. Der gebrokerte LLM-Key muss dagegen ankommen.
func TestSubAgentEnvIsHermetic(t *testing.T) {
	bin, home := fakeClaude(t, `
printf 'port=%s key=%s\n' "$COVEY_ACTION_PORT" "$ANTHROPIC_API_KEY" > "$HOME/env.txt"
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
		t.Fatalf("Sub-Lauf darf keinen Action-Proxy sehen: %q", got)
	}
	if !strings.Contains(string(got), "sk-ant-api-geheim") {
		t.Fatalf("gebrokerter LLM-Key fehlt: %q", got)
	}
}
