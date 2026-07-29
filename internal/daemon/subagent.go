package daemon

import (
	"context"
	"encoding/json"
	"fmt"
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
// programmiert. Er läuft deshalb bewusst **hermetisch**: kein
// COVEY_ACTION_PORT, also kein Zugriff auf Zielsysteme. Er kann lesen, ändern,
// bauen und testen — mehr nicht.
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
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(c.homeDir, dir)
	}

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

	res, err := runtime.Run(ctx, spec, func(kind string, payload json.RawMessage) {
		_ = c.send(TypeEvent, Event{TaskID: taskID, Kind: kind, Payload: markSubAgent(payload, dir)})
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

// markSubAgent markiert eine Runtime-Zeile als Teil eines Sub-Laufs — als
// zusätzlichen Schlüssel IM Objekt, nicht als Hülle darum. Der Unterschied ist
// nicht kosmetisch: Aufzeichnung und Timeline lesen das Format der Runtime
// (stream-json) direkt, und eine Hülle würde `type` verdecken. Der Sub-Lauf
// stünde dann als JSON-Klumpen in der Aufzeichnung statt als Turn mit seinen
// Tool-Aufrufen — ausgerechnet dort, wo die eigentliche Arbeit passiert.
func markSubAgent(payload json.RawMessage, dir string) json.RawMessage {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(payload, &obj); err != nil || obj == nil {
		return payload // kein JSON-Objekt → unverändert durchreichen
	}
	mark, err := json.Marshal(map[string]string{"dir": dir})
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
	out, err := exec.CommandContext(ctx, "git", "-C", dir,
		"rev-parse", "--verify", "--quiet", rev+"^{commit}").Output()
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
		case strings.HasPrefix(code, "R"), strings.HasPrefix(code, "C"):
			// Umbenennung/Kopie: das Ziel ist neu, bei R fällt die Quelle weg.
			// Die Quelle muss mit, sonst bliebe sie im Zielsystem stehen.
			if to != "" {
				state[to] = false
			}
			if strings.HasPrefix(code, "R") {
				state[from] = true
			}
		case strings.ContainsRune(code, 'D'):
			state[from] = true
		default:
			state[from] = false
		}
	}
	// Committet: --name-status trennt mit Tabs, Umbenennungen als "R100 alt neu".
	for _, line := range gitLines(ctx, dir, "diff", "--name-status", base, "HEAD") {
		f := strings.Split(line, "\t")
		if len(f) < 2 {
			continue
		}
		to := ""
		if len(f) > 2 {
			to = f[2]
		}
		mark(f[0], f[1], to)
	}
	// Offen im Arbeitsverzeichnis: --porcelain, Umbenennungen als "alt -> neu".
	for _, line := range gitLines(ctx, dir, "status", "--porcelain", "-uall") {
		if len(line) < 4 {
			continue
		}
		code, path := strings.TrimSpace(line[:2]), strings.TrimSpace(line[3:])
		from, to := path, ""
		if i := strings.Index(path, " -> "); i >= 0 {
			from, to = path[:i], path[i+4:]
		}
		mark(code, strings.Trim(from, `"`), strings.Trim(to, `"`))
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

// gitLines führt ein git-Kommando aus und liefert seine Ausgabezeilen. Ohne git
// oder ohne Repository leer: Dann meldet der Sub-Lauf eben keine Dateiliste,
// statt zu scheitern.
func gitLines(ctx context.Context, dir string, args ...string) []string {
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return nil
	}
	trimmed := strings.TrimRight(string(out), "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}
