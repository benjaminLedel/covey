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

// Umlaute, Leerzeichen und Anführungszeichen in Pfaden müssen ROH im Bericht
// ankommen: Die Listen gehen unverändert in die commit-Aktion. git escaped
// Pfade außerhalb ASCII per Default (core.quotepath=true, so steht es in der
// Sandbox) — aus prüfung.go würde "pr\303\274fung.go", und genau dieser
// verstümmelte Pfad landete im Zielsystem.
func TestGitChangesSinceRawPaths(t *testing.T) {
	dir := gitRepo(t)
	// Den Default der Sandbox lokal festschreiben, damit der Test unabhängig
	// von der globalen Konfiguration des Entwicklerrechners reproduziert.
	runGit(t, dir, "config", "core.quotepath", "true")

	writeFile(t, dir, "alt ümlaut.go", "package app")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "covey baseline")
	runGit(t, dir, "tag", target.BaselineRef)
	base := gitRev(t.Context(), dir, target.BaselineRef)

	// Committet: eine Umbenennung, Umlaut und Leerzeichen auf beiden Seiten.
	runGit(t, dir, "mv", "alt ümlaut.go", "neu geprüft.go")
	runGit(t, dir, "commit", "-q", "-m", "umbenannt")
	// Offen im Arbeitsverzeichnis: eine neue Datei, ebenfalls mit Umlaut.
	writeFile(t, dir, "änderung offen.txt", "x")

	changed, deleted := gitChangesSince(t.Context(), dir, base)
	if want := []string{"neu geprüft.go", "änderung offen.txt"}; !reflect.DeepEqual(changed, want) {
		t.Fatalf("changed muss die rohen Pfade nennen: %q (erwartet %q)", changed, want)
	}
	if want := []string{"alt ümlaut.go"}; !reflect.DeepEqual(deleted, want) {
		t.Fatalf("Quelle der Umbenennung muss roh mitkommen: %q (erwartet %q)", deleted, want)
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
// subAgentMarkOf liest die Marke aus einer markierten Zeile.
func subAgentMarkOf(t *testing.T, line json.RawMessage) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(line, &obj); err != nil {
		t.Fatalf("markierte Zeile ist kein JSON: %s", line)
	}
	mark, ok := obj["covey_sub_agent"].(map[string]any)
	if !ok {
		t.Fatalf("Sub-Lauf-Markierung fehlt: %s", line)
	}
	return mark
}

func TestMarkSubAgentKeepsStreamFormat(t *testing.T) {
	got, stamped := markSubAgent(json.RawMessage(`{"type":"assistant","message":{"content":[]}}`), "/home/agent/repos/p", "7", "")
	if !stamped {
		t.Fatal("ein JSON-Objekt muss markiert werden")
	}

	var obj map[string]any
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatal(err)
	}
	if obj["type"] != "assistant" {
		t.Fatalf("type muss oben stehen bleiben: %s", got)
	}
	mark := subAgentMarkOf(t, got)
	if mark["dir"] != "/home/agent/repos/p" || mark["run"] != "7" {
		t.Fatalf("Markierung unvollständig: %s", got)
	}
	if _, ok := mark["task"]; ok {
		t.Fatalf("ohne Auftrag darf kein task-Schlüssel entstehen: %s", got)
	}
}

// Zeilen, die kein JSON-Objekt sind, lässt der Adapter bewusst durch. Sie
// dürfen weder verändert noch als markiert gemeldet werden — sonst gälte der
// Auftrag als untergebracht, obwohl ihn niemand trägt.
func TestMarkSubAgentPassesNonObjects(t *testing.T) {
	for _, line := range []string{"kein json", `["liste"]`, `null`} {
		got, stamped := markSubAgent(json.RawMessage(line), "/repo", "1", "Auftrag")
		if stamped || string(got) != line {
			t.Fatalf("%q muss unverändert und unmarkiert durchgehen, war %q (stamped=%v)", line, got, stamped)
		}
	}
}

// Der Arbeitsauftrag ist die Überschrift des Sub-Laufs in der Timeline. Er
// steht deshalb in der Marke — gekürzt, weil er dort nur betiteln soll.
func TestMarkSubAgentCarriesTask(t *testing.T) {
	got, _ := markSubAgent(json.RawMessage(`{"type":"system","subtype":"init"}`), "/repo", "1", "  Nullpointer beheben  ")
	if task := subAgentMarkOf(t, got)["task"]; task != "Nullpointer beheben" {
		t.Fatalf("Auftrag muss getrimmt in der Marke stehen: %s", got)
	}

	// Lange Aufträge werden rune-sicher gekürzt — kein halbes Zeichen, keine
	// Timeline, die am Auftragstext erstickt.
	got, _ = markSubAgent(json.RawMessage(`{"type":"system"}`), "/repo", "1", strings.Repeat("ä", maxMarkedTask+50))
	task, _ := subAgentMarkOf(t, got)["task"].(string)
	if r := []rune(task); len(r) != maxMarkedTask+1 || r[len(r)-1] != '…' {
		t.Fatalf("Auftrag muss auf %d Runen plus Auslassung gekürzt werden, war %d", maxMarkedTask, len([]rune(task)))
	}
}

// Der Kern der Auszeichnung: dir und run stehen auf JEDER Zeile eines Laufs —
// daran erkennt die Timeline seine Zeilen, auch wenn ein zweiter Sub-Lauf
// dazwischenfunkt. Der Auftrag dagegen steht genau einmal, sonst ginge er mit
// seiner Länge × Zeilenzahl in die Aufzeichnung.
func TestSubAgentStamperGivesTaskOnce(t *testing.T) {
	stamp := subAgentStamper("/repo", "3", "Nullpointer beheben")

	// Eine Nicht-JSON-Zeile zuerst: Sie darf den Auftrag nicht aufbrauchen.
	if got := stamp(json.RawMessage("Warnung aus dem Wrapper")); string(got) != "Warnung aus dem Wrapper" {
		t.Fatalf("Nicht-JSON muss unverändert durchgehen: %s", got)
	}

	var withTask int
	for i := range 3 {
		mark := subAgentMarkOf(t, stamp(json.RawMessage(`{"type":"assistant"}`)))
		if mark["dir"] != "/repo" || mark["run"] != "3" {
			t.Fatalf("Zeile %d ohne vollständige Lauf-Kennung: %v", i, mark)
		}
		if _, ok := mark["task"]; ok {
			withTask++
		}
	}
	if withTask != 1 {
		t.Fatalf("genau eine Zeile trägt den Auftrag, waren %d", withTask)
	}
}

// Zwei Sub-Läufe desselben Daemons müssen unterscheidbare Kennungen bekommen —
// sonst verschmelzen sie in der Timeline zu einem Block.
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
		t.Fatal("jeder Sub-Lauf braucht eine eigene Kennung")
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
// Zielsystemen — und auch nicht über die Hintertür der Daemon-Umgebung. Sie
// enthält COVEY_WS_URL und COVEY_DAEMON_TOKEN; damit könnte ein Hook oder
// MCP-Server aus dem Repo eine eigene WebSocket zur Control Plane öffnen und
// selbst `request_credential` schicken, also genau die gebrokerten Zugänge
// erreichen, die der fehlende Action-Proxy fernhält. Der gebrokerte LLM-Key
// muss dagegen ankommen, sonst läuft die Runtime nicht.
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
		t.Fatalf("Sub-Lauf darf keinen Action-Proxy sehen: %q", got)
	}
	if !strings.Contains(string(got), "sk-ant-api-geheim") {
		t.Fatalf("gebrokerter LLM-Key fehlt: %q", got)
	}
	if !strings.Contains(string(got), "token= ws=") {
		t.Fatalf("Zugangsdaten des Daemons dürfen nicht in den Sub-Lauf erben: %q", got)
	}
}

// Ein Sub-Lauf je Checkout. Zwei parallele Läufe im selben Verzeichnis
// schrieben sich gegenseitig die Dateien um und meldeten beide denselben
// kumulativen Stand — der beauftragende Agent bekäme zwei Berichte über eine
// vermischte Arbeit. Verschiedene Verzeichnisse bleiben parallel möglich.
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

	// Belegt, wie es ein noch laufender Sub-Agent hinterlässt.
	if err := c.claimSubAgentDir(home); err != nil {
		t.Fatal(err)
	}
	if _, err := c.runSubAgent(t.Context(), "task-1", target.SubAgentRequest{Dir: home, Task: "x"}); err == nil {
		t.Fatal("zweiter Auftrag im selben Checkout muss abgelehnt werden")
	}
	if _, err := c.runSubAgent(t.Context(), "task-1", target.SubAgentRequest{Dir: other, Task: "x"}); err != nil {
		t.Fatalf("ein anderer Checkout ist davon nicht betroffen: %v", err)
	}
	// Nach dem Bericht ist das Verzeichnis wieder frei.
	c.releaseSubAgentDir(home)
	if _, err := c.runSubAgent(t.Context(), "task-1", target.SubAgentRequest{Dir: home, Task: "x"}); err != nil {
		t.Fatalf("nach Abschluss muss derselbe Checkout wieder gehen: %v", err)
	}
}

// Ein cwd, das es nicht gibt, scheitert mit einer Meldung im Stil der anderen
// Validierungen — nicht erst am chdir des Subprozesses („claude starten:
// chdir …"), was dem Agenten nichts über seinen Fehler sagt.
func TestSubAgentRejectsMissingDir(t *testing.T) {
	bin, home := fakeClaude(t, `
cat <<'EOF'
{"type":"result","subtype":"success","session_id":"s","result":"fertig"}
EOF`)
	c := &Client{homeDir: home, runtimes: map[string]Runtime{"claude-code": &ClaudeCode{Binary: bin}},
		creds: map[string]InjectCredentials{}, cfg: InjectConfig{Runtime: "claude-code"}}

	_, err := c.runSubAgent(t.Context(), "task-1",
		target.SubAgentRequest{Dir: "repos/gibts-nicht", Task: "x"})
	if err == nil || !strings.Contains(err.Error(), "checkout-Ergebnis") {
		t.Fatalf("fehlendes cwd muss vorab abgelehnt werden, war: %v", err)
	}
}
