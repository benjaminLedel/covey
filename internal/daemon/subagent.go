package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"covey/internal/target"
)

// Ein Sub-Lauf ist ein geschachtelter Runtime-Lauf, der IM Projekt-Checkout
// startet statt im Agenten-Home. Damit greift dort der Claude-Code-Harness des
// Projekts selbst — CLAUDE.md als Projekt-Memory, `.claude/agents`, Skills und
// Commands —, den der äußere Lauf nie sieht, weil er vom Home aus läuft.
//
// Die Rollenteilung dahinter (spec/12): Der äußere Agent ist Orchestrator und
// Kommunikator (Triage, Issue-/MR-Verkehr, commit, Gedächtnis), der Sub-Agent
// programmiert. Er erreicht deshalb bewusst keine Zielsysteme: kein
// COVEY_ACTION_PORT — und über childEnv auch nicht die Zugangsdaten des
// Daemons zur Control Plane, mit denen sich der Broker direkt ansprechen
// ließe. Er kann lesen, ändern, bauen und testen — mehr nicht.
const (
	// defaultSubAgentTurns ist großzügiger als das Limit des äußeren Laufs:
	// Der Sub-Agent macht die eigentliche Arbeit (verstehen, ändern, testen).
	defaultSubAgentTurns = 60
	maxSubAgentTurns     = 200
)

// subAgentPrompt ist der einzige Plattform-Anteil am Prompt des Sub-Laufs.
// Bewusst knapp: Der Harness des Projekts soll dominieren, nicht Covey.
const subAgentPrompt = `Du arbeitest im Checkout eines Projekts und bist für genau einen Arbeitsauftrag zuständig.
Die Konventionen dieses Projekts gelten: Halte dich an CLAUDE.md, CONTRIBUTING und die
Regeln, Skills und Subagenten, die das Projekt mitbringt.

Rahmen deiner Arbeit:
- Du hast KEINEN Zugang zu GitLab, E-Mail oder anderen Zielsystemen und kannst nicht pushen.
  Lokale git-Commits im Checkout sind in Ordnung (halte dich an die Konventionen des Projekts);
  das Einchecken ins Zielsystem übernimmt der Agent, der dich beauftragt hat.
- Verifiziere deine Änderung, bevor du fertig meldest: Build bzw. Tests des Projekts ausführen,
  für einen Fix möglichst einen Test ergänzen.
- Deine letzte Nachricht ist dein Bericht an den beauftragenden Agenten. Fasse darin zusammen:
  Ursache, was du geändert hast (Datei:Zeile), wie du es verifiziert hast (welche Kommandos, welches
  Ergebnis) und was offen blieb. Kein Status-Marker, keine Floskeln — der Bericht ist die Übergabe.`

// subAgentRunner bindet den Runner an die laufende Aufgabe (für Recording und
// Kostenzuordnung) und liefert ihn in der Form, die der target-Port erwartet.
func (c *Client) subAgentRunner(taskID string) target.SubAgentRunner {
	return func(ctx context.Context, req target.SubAgentRequest) (target.SubAgentResult, error) {
		return c.runSubAgent(ctx, taskID, req)
	}
}

// runSubAgent fährt einen geschachtelten Runtime-Lauf im angegebenen
// Verzeichnis. Events und Kosten laufen über dieselben Protokoll-Nachrichten
// wie der äußere Lauf: Die Timeline zeigt den Sub-Lauf (markiert), und
// AddCost/enforceBudget der Control Plane greifen auch hier — ein Sub-Lauf
// kann das Budget also nicht umgehen.
func (c *Client) runSubAgent(ctx context.Context, taskID string, req target.SubAgentRequest) (target.SubAgentResult, error) {
	task := strings.TrimSpace(req.Task)
	if task == "" {
		return target.SubAgentResult{}, fmt.Errorf("task fehlt: der Sub-Agent braucht einen Arbeitsauftrag")
	}
	dir := req.Dir
	if dir == "" {
		return target.SubAgentResult{}, fmt.Errorf("cwd fehlt: der Sub-Agent startet im Projekt-Checkout")
	}
	dir = filepath.Clean(dir)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(c.homeDir, dir)
	}
	// Pfad vorab prüfen, statt den Lauf am chdir des Subprozesses scheitern zu
	// lassen: „claude starten: chdir …" sagt dem Agenten nicht, was er falsch
	// gemacht hat.
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return target.SubAgentResult{}, fmt.Errorf(
			"cwd %q ist kein Verzeichnis in der Sandbox — nimm den Pfad aus dem checkout-Ergebnis", req.Dir)
	}
	// Ein Sub-Lauf je Verzeichnis. Der Action-Proxy bedient jede Anfrage in
	// einer eigenen Goroutine und die Runtime ruft Werkzeuge durchaus parallel
	// auf: Zwei Läufe im selben Checkout schrieben sich gegenseitig die Dateien
	// um und meldeten beide denselben kumulativen Stand — zwei Berichte über
	// eine vermischte Arbeit. Deshalb Absage statt Warteschlange: Der Agent
	// soll den ersten Bericht lesen, bevor er im selben Checkout den nächsten
	// Auftrag gibt. Verschiedene Verzeichnisse laufen weiter parallel.
	if err := c.claimSubAgentDir(dir); err != nil {
		return target.SubAgentResult{}, err
	}
	defer c.releaseSubAgentDir(dir)

	c.mu.Lock()
	cfg := c.cfg
	c.mu.Unlock()

	runtime := c.runtimes[cfg.Runtime]
	if runtime == nil {
		return target.SubAgentResult{}, fmt.Errorf("unbekannte runtime %q", cfg.Runtime)
	}

	turns := req.MaxTurns
	if turns <= 0 {
		turns = defaultSubAgentTurns
	}
	if turns > maxSubAgentTurns {
		turns = maxSubAgentTurns
	}
	model := req.Model
	if model == "" {
		model = cfg.Model
	}

	// Anker für den Bericht: der Upstream-Stand, den der Checkout als Tag
	// festhält. Fehlt er (das Verzeichnis kommt nicht aus einem Checkout),
	// dient der Stand unmittelbar vor dem Sub-Lauf als Anker — nie das
	// Wurzel-Commit, sonst wäre in einem echten Klon die ganze Repo-Historie
	// „Arbeit".
	base := gitRev(ctx, dir, target.BaselineRef)
	if base == "" {
		base = gitRev(ctx, dir, "HEAD")
	}

	spec := RunSpec{
		TaskID:       taskID,
		Title:        "Arbeitsauftrag im Projekt",
		Body:         task,
		SystemPrompt: subAgentPrompt,
		Model:        model,
		AllowedTools: cfg.AllowedTools,
		MaxTurns:     turns,
		HomeDir:      c.homeDir,
		WorkDir:      dir,
		// Bewusst ohne COVEY_ACTION_PORT: hermetisch, keine Zielsysteme.
		Env: c.runtimeKeyEnv(),
	}

	// Der Arbeitsauftrag reist nur auf der ERSTEN Zeile mit: Er ist die
	// Überschrift des Sub-Laufs in der Aufzeichnung, und auf jede Zeile
	// gestempelt würde er die Timeline um seine Länge × Zeilenzahl aufblähen.
	// Der Callback läuft sequentiell in der Scanner-Schleife von stream — das
	// Flag braucht deshalb keine Synchronisierung.
	head := task
	res, err := runtime.Run(ctx, spec, func(kind string, payload json.RawMessage) {
		_ = c.send(TypeEvent, Event{TaskID: taskID, Kind: kind, Payload: markSubAgent(payload, dir, head)})
		head = ""
	})
	if err != nil {
		return target.SubAgentResult{}, err
	}
	if res.CostUSD > 0 || res.InputTokens > 0 {
		_ = c.send(TypeCost, Cost{TaskID: taskID, USD: res.CostUSD,
			InputTokens: res.InputTokens, OutputTokens: res.OutputTokens, Model: res.Model})
	}

	changed, deleted := gitChangesSince(ctx, dir, base)
	return target.SubAgentResult{
		// Beim Turn-Limit liefert der Adapter statt eines Ergebnisses den
		// Übergabe-Stand der abgebrochenen Session (Status "incomplete",
		// siehe runtime_claudecode.go). Genau der gehört in den Bericht: Der
		// beauftragende Agent schließt mit dem Teilergebnis ab und legt den
		// Rest als Aufgabe an, statt die halbe Arbeit zu verlieren.
		Result:         res.Result,
		ChangedFiles:   changed,
		Deleted:        deleted,
		CostUSD:        res.CostUSD,
		Error:          res.Error,
		TurnsExhausted: res.Status == "incomplete",
	}, nil
}

// claimSubAgentDir belegt ein Verzeichnis für die Dauer eines Sub-Laufs und
// lehnt einen zweiten Lauf darin ab, solange der erste arbeitet.
func (c *Client) claimSubAgentDir(dir string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subAgentDirs == nil {
		c.subAgentDirs = map[string]bool{}
	}
	if c.subAgentDirs[dir] {
		return fmt.Errorf("in %s arbeitet schon ein Sub-Agent — lies erst seinen Bericht, "+
			"bevor du im selben Checkout den nächsten Auftrag gibst", dir)
	}
	c.subAgentDirs[dir] = true
	return nil
}

func (c *Client) releaseSubAgentDir(dir string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.subAgentDirs, dir)
}

// maxMarkedTask begrenzt den Arbeitsauftrag in der Marke. Er dient dort als
// Überschrift, nicht als Archiv — den vollen Text hält ohnehin das
// action-Event des Action-Proxys fest.
const maxMarkedTask = 400

// markSubAgent markiert eine Runtime-Zeile als Teil eines Sub-Laufs — als
// zusätzlichen Schlüssel IM Objekt, nicht als Hülle darum. Der Unterschied ist
// nicht kosmetisch: Aufzeichnung und Timeline lesen das Format der Runtime
// (stream-json) direkt, und eine Hülle würde `type` verdecken. Der Sub-Lauf
// stünde dann als JSON-Klumpen in der Aufzeichnung statt als Turn mit seinen
// Tool-Aufrufen — ausgerechnet dort, wo die eigentliche Arbeit passiert.
//
// task ist optional und steht nur auf der ersten Zeile eines Sub-Laufs (siehe
// runSubAgent): Die Timeline zeigt damit im Kopf des Blocks, WOMIT der
// Sub-Agent beauftragt war, ohne dass man ihn aufklappen muss.
func markSubAgent(payload json.RawMessage, dir, task string) json.RawMessage {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(payload, &obj); err != nil || obj == nil {
		return payload // kein JSON-Objekt → unverändert durchreichen
	}
	fields := map[string]string{"dir": dir}
	if task = strings.TrimSpace(task); task != "" {
		if r := []rune(task); len(r) > maxMarkedTask {
			task = string(r[:maxMarkedTask]) + "…"
		}
		fields["task"] = task
	}
	mark, err := json.Marshal(fields)
	if err != nil {
		return payload
	}
	obj["covey_sub_agent"] = mark
	marked, err := json.Marshal(obj)
	if err != nil {
		return payload
	}
	return marked
}

// gitRev löst eine Referenz zu einem Commit auf. Leer, wenn es sie nicht gibt
// (kein Repo, kein Commit, kein Tag) — der Aufrufer entscheidet dann, ob er
// einen anderen Anker nimmt oder gar keine Dateiliste meldet.
func gitRev(ctx context.Context, dir, rev string) string {
	out, err := gitRun(ctx, dir, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitChangesSince liefert die Arbeit im Checkout als die beiden Listen, die die
// commit-Aktion erwartet: geänderte/neue und gelöschte Dateien, jeweils
// repo-relativ und gemessen gegen base.
//
// Gemessen wird gegen einen COMMIT, nicht gegen ein `git status`-Abbild von
// vorher. Das ist der Punkt: Der Sub-Agent darf im Checkout lokal committen —
// viele Projekte verlangen das in ihrer CLAUDE.md —, und nach einem Commit
// zeigt `git status` nichts mehr an. Die Arbeit läge dann fertig auf Platte,
// aber der Bericht wäre leer und die commit-Aktion bräche mit „nichts zu
// committen" ab.
//
// Deshalb beide Hälften zusammen: was seit base committet wurde (git diff) und
// was daneben im Arbeitsverzeichnis offen liegt (git status). Cache-
// Verzeichnisse bleiben über .git/info/exclude des Checkouts außen vor.
func gitChangesSince(ctx context.Context, dir, base string) (changed, deleted []string) {
	if base == "" {
		return nil, nil // kein Anker → lieber keine Liste als eine falsche
	}
	// Pfad → gelöscht? Die spätere Quelle gewinnt, weil sie den jüngeren Stand
	// beschreibt: erst base→HEAD, dann HEAD→Arbeitsverzeichnis.
	state := map[string]bool{}
	mark := func(code, from, to string) {
		switch {
		case code == "" || from == "":
			return
		case strings.ContainsAny(code, "RC"):
			// Umbenennung/Kopie: das Ziel ist neu, bei R fällt die Quelle weg.
			// Die Quelle muss mit, sonst bliebe sie im Zielsystem stehen.
			if to != "" {
				state[to] = false
			}
			if strings.ContainsRune(code, 'R') {
				state[from] = true
			}
		case strings.ContainsRune(code, 'D'):
			state[from] = true
		default:
			state[from] = false
		}
	}
	// Committet: --name-status -z liefert einen Feldstrom aus Status und Pfad,
	// bei Umbenennung/Kopie zwei Pfaden — "R100\0alt\0neu\0".
	fields := gitFields(ctx, dir, "diff", "--name-status", "-z", base, "HEAD")
	for i := 0; i+1 < len(fields); {
		code, from := fields[i], fields[i+1]
		i += 2
		to := ""
		if strings.ContainsAny(code, "RC") {
			if i >= len(fields) {
				break
			}
			to = fields[i]
			i++
		}
		mark(code, from, to)
	}
	// Offen im Arbeitsverzeichnis: --porcelain -z. Ein Feld ist ein Datensatz
	// "XY <pfad>"; bei Umbenennung/Kopie folgt die Quelle als eigenes Feld —
	// und zwar NACH dem Ziel, denn in -z ist die Reihenfolge gegenüber
	// "alt -> neu" umgekehrt.
	fields = gitFields(ctx, dir, "status", "--porcelain", "-z", "-uall")
	for i := 0; i < len(fields); {
		rec := fields[i]
		i++
		if len(rec) < 4 {
			continue
		}
		code, path := strings.TrimSpace(rec[:2]), rec[3:]
		if strings.ContainsAny(code, "RC") {
			if i >= len(fields) {
				break
			}
			mark(code, fields[i], path) // Ziel zuerst, Quelle danach
			i++
			continue
		}
		mark(code, path, "")
	}

	for path, gone := range state {
		if gone {
			deleted = append(deleted, path)
			continue
		}
		changed = append(changed, path)
	}
	// Stabil halten — sonst wechselt die Reihenfolge je Lauf (Map-Iteration)
	// und Recording wie Commit-Diff werden unnötig rauschig.
	sort.Strings(changed)
	sort.Strings(deleted)
	return changed, deleted
}

// gitRun führt ein git-Kommando im Checkout aus. Zwei Details stecken hier
// drin, beide nicht kosmetisch:
//
//   - core.quotepath=false: Sonst liefert git (Default true) Pfade außerhalb
//     ASCII escaped — "pr\303\274fung.go" statt prüfung.go. Der Pfad ginge
//     verstümmelt in changed_files und von dort unverändert in die
//     commit-Aktion. Zusammen mit -z beim Aufrufer kommen die Bytes roh und
//     ungequotet heraus; das erledigt Leerzeichen und Anführungszeichen mit.
//   - childEnv: ohne die COVEY_*-Variablen des Daemons. git liest Konfiguration
//     AUS dem Repository, und die kann Kommandos benennen, die git selbst
//     ausführt (core.fsmonitor, Filter-Driver). Nach dem Sub-Lauf ist dieses
//     Repository nicht vertrauenswürdiger als der übrige Checkout.
func gitRun(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.quotepath=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = childEnv()
	return cmd.Output()
}

// gitFields führt ein git-Kommando mit -z aus und liefert dessen
// NUL-getrennte Felder. Ohne git oder ohne Repository leer: Dann meldet der
// Sub-Lauf eben keine Dateiliste, statt zu scheitern.
func gitFields(ctx context.Context, dir string, args ...string) []string {
	out, err := gitRun(ctx, dir, args...)
	if err != nil {
		return nil
	}
	trimmed := strings.TrimRight(string(out), "\x00")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\x00")
}
