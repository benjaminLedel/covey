package daemon

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"covey/internal/target"
)

// TestGitChanges prüft die Abbildung des git-Status auf die beiden Listen der
// commit-Aktion. Entscheidend ist die Differenz zum Stand VOR dem Sub-Lauf:
// Was der Agent vorher schon geändert hatte (oder was ein abgebrochener
// Vorlauf hinterlassen hat), darf der Bericht nicht als neue Arbeit ausgeben.
func TestGitChanges(t *testing.T) {
	before := map[string]string{"vorher.go": "M"}
	after := map[string]string{
		"vorher.go":    "M",  // unverändert seit vorher → nicht melden
		"pkg/app.go":   "M",  // geändert
		"pkg/neu.go":   "??", // neu angelegt
		"alt.go":       "D",  // gelöscht
		"umbenannt.go": "R",
	}
	changed, deleted := gitChanges(before, after)

	wantChanged := []string{"pkg/app.go", "pkg/neu.go", "umbenannt.go"}
	if !reflect.DeepEqual(changed, wantChanged) {
		t.Fatalf("changed falsch: %v (erwartet %v)", changed, wantChanged)
	}
	if !reflect.DeepEqual(deleted, []string{"alt.go"}) {
		t.Fatalf("deleted falsch: %v", deleted)
	}
}

// Ohne git (Verzeichnis ist kein Repo) liefert gitStatus nichts, statt zu
// scheitern — der Sub-Lauf meldet dann eben keine Dateiliste.
func TestGitStatusWithoutRepo(t *testing.T) {
	if got := gitStatus(t.Context(), t.TempDir()); len(got) != 0 {
		t.Fatalf("ohne Repository darf nichts gemeldet werden: %v", got)
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
