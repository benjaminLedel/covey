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
- Du hast KEINEN Zugang zu GitLab, E-Mail oder anderen Zielsystemen und kannst nicht committen
  oder pushen. Ändere die Dateien lokal — das Einchecken übernimmt der Agent, der dich beauftragt hat.
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

	// Ausgangsstand festhalten, damit der Bericht die geänderten Dateien
	// nennen kann — genau in der Form, die die commit-Aktion erwartet.
	before := gitStatus(ctx, dir)

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
		// Als Sub-Lauf markieren, damit die Timeline äußeren und inneren Lauf
		// auseinanderhalten kann.
		wrapped, mErr := json.Marshal(map[string]any{"sub_agent": true, "dir": dir, "event": payload})
		if mErr != nil {
			wrapped = payload
		}
		_ = c.send(TypeEvent, Event{TaskID: taskID, Kind: kind, Payload: wrapped})
	})
	if err != nil {
		return target.SubAgentResult{}, err
	}
	if res.CostUSD > 0 || res.InputTokens > 0 {
		_ = c.send(TypeCost, Cost{TaskID: taskID, USD: res.CostUSD,
			InputTokens: res.InputTokens, OutputTokens: res.OutputTokens, Model: res.Model})
	}

	changed, deleted := gitChanges(before, gitStatus(ctx, dir))
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

// gitStatus liest den Arbeitsstand des Checkouts als Pfad→Status-Abbild.
// Der Checkout legt eine git-Baseline an (siehe target/gitlab/checkout.go) und
// schließt die Cache-Verzeichnisse über .git/info/exclude aus — was hier
// auftaucht, ist echte Arbeit. Ohne git (Verzeichnis ist kein Repo) leer:
// dann meldet der Sub-Lauf eben keine Dateiliste, statt zu scheitern.
func gitStatus(ctx context.Context, dir string) map[string]string {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "status", "--porcelain", "-uall").Output()
	if err != nil {
		return nil
	}
	state := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 4 {
			continue
		}
		code, path := strings.TrimSpace(line[:2]), strings.TrimSpace(line[3:])
		// Umbenennungen meldet git als "alt -> neu"; uns interessiert das Ziel.
		if idx := strings.Index(path, " -> "); idx >= 0 {
			path = path[idx+4:]
		}
		state[strings.Trim(path, `"`)] = code
	}
	return state
}

// gitChanges bildet die Differenz zweier Statusabbilder auf die beiden Listen
// der commit-Aktion ab: geänderte/neue Dateien und gelöschte.
func gitChanges(before, after map[string]string) (changed, deleted []string) {
	for path, code := range after {
		if before[path] == code {
			continue // stand schon vor dem Sub-Lauf so da
		}
		if strings.Contains(code, "D") {
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
