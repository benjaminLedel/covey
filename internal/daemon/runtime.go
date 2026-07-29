package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// DefaultAllowedTools ist der Standard-Tool-Umfang eines Laufs, solange keine
// Agent-Config etwas anderes vorgibt: Dateien lesen/schreiben/suchen, Shell,
// Webseiten abrufen und durchsuchen, Aufgaben strukturieren. Die harte Grenze
// bleibt außerhalb der Runtime (Broker, Egress, Guard-Rails) — diese Liste ist
// die weiche Innen-Grenze aus spec/12.
var DefaultAllowedTools = []string{
	"Bash", "BashOutput", "KillShell",
	"Read", "Write", "Edit", "Glob", "Grep", "NotebookEdit",
	"WebFetch", "WebSearch",
	"Task", "TodoWrite",
}

// RunSpec ist alles, was ein Runtime-Adapter für einen Lauf braucht.
type RunSpec struct {
	TaskID          string
	Title           string
	Body            string
	SystemPrompt    string
	MemoryContext   string
	Model           string // gewünschtes LLM; leer = Default der Runtime
	AllowedTools    []string
	MaxTurns        int
	MaxBudgetUSD    float64
	ResumeSessionID string
	ResumeInput     string
	HomeDir         string
	// WorkDir ist das Arbeitsverzeichnis des Laufs. Leer = HomeDir. Getrennt
	// vom Home, damit ein Sub-Run IM Projekt-Checkout starten kann (dort greift
	// dessen eigener Claude-Code-Harness: CLAUDE.md, .claude/agents, skills),
	// während HOME weiterhin auf das persistente Agenten-Home zeigt — dort
	// liegen ~/.claude, die Wiki-Arbeitskopie und die Dependency-Caches.
	WorkDir string
	Env     []string // zusätzliche ENV (z. B. COVEY_ACTION_PORT, gebrokerte Keys)
}

// RunResult ist das normierte Ergebnis eines Runtime-Laufs.
type RunResult struct {
	Status         string // done | failed | escalated | blocked
	Result         string
	Error          string
	CorrelationKey string
	Question       string
	Memory         string
	SessionID      string
	CostUSD        float64
	InputTokens    int64
	OutputTokens   int64
	Model          string
}

// Runtime ist der Adapter-Port (spec/01): dünn, übersetzt zwischen dem
// Daemon-Protokoll und den Spezifika der jeweiligen Runtime.
type Runtime interface {
	Name() string
	Run(ctx context.Context, spec RunSpec, onEvent func(kind string, payload json.RawMessage)) (RunResult, error)
}

// covey_status ist die Abschluss-Zeile, die die kompilierte Config von der
// Runtime verlangt (siehe agents.ProtocolInstructions).
type covey_status struct {
	Status         string `json:"status"`
	Result         string `json:"result"`
	CorrelationKey string `json:"correlation_key"`
	Question       string `json:"question"`
	Memory         string `json:"memory"`
}

const statusMarker = "COVEY_STATUS:"

// ParseStatusLine sucht die letzte COVEY_STATUS-Zeile im Output.
// Fehlt sie, gilt der Lauf als done mit dem Gesamttext als Ergebnis —
// fail-open wäre falsch bei blocked, aber done ohne Marker ist das
// gutmütige Default für triviale Aufgaben.
func ParseStatusLine(output string) (covey_status, bool) {
	idx := strings.LastIndex(output, statusMarker)
	if idx < 0 {
		return covey_status{}, false
	}
	rest := strings.TrimSpace(output[idx+len(statusMarker):])
	// Nur die JSON-Zeile, nicht eventuell folgender Text.
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[:nl]
	}
	var st covey_status
	if err := json.Unmarshal([]byte(rest), &st); err != nil {
		return covey_status{}, false
	}
	if st.Status == "" {
		return covey_status{}, false
	}
	return st, true
}

// applyStatus überträgt die COVEY_STATUS-Zeile in das RunResult.
func applyStatus(res *RunResult, output string) {
	st, ok := ParseStatusLine(output)
	if !ok {
		res.Status = "done"
		res.Result = strings.TrimSpace(output)
		return
	}
	switch st.Status {
	case "blocked":
		if st.CorrelationKey == "" {
			// blocked ohne Key kann nie geweckt werden → als failed behandeln.
			res.Status = "failed"
			res.Error = "runtime meldete blocked ohne correlation_key"
			return
		}
		res.Status = "blocked"
		res.CorrelationKey = st.CorrelationKey
		res.Question = st.Question
	case "done", "escalated":
		res.Status = st.Status
		res.Result = st.Result
		res.Memory = st.Memory
	default:
		res.Status = "failed"
		res.Error = fmt.Sprintf("unbekannter COVEY_STATUS %q", st.Status)
	}
}
