package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ClaudeCode steuert Claude Code headless über `claude -p` (spec/12):
// Prompt rein, stream-json-Events raus, `--resume <session_id>` für die
// blocked→working-Kante.
type ClaudeCode struct {
	// Binary ist der CLI-Pfad; Default "claude", überschreibbar via ENV
	// COVEY_CLAUDE_BIN (so testen wir den Adapter gegen ein Fake-Binary).
	Binary string
}

func NewClaudeCode() *ClaudeCode {
	bin := os.Getenv("COVEY_CLAUDE_BIN")
	if bin == "" {
		bin = "claude"
	}
	return &ClaudeCode{Binary: bin}
}

func init() {
	RegisterRuntime(RuntimeDescriptor{
		Name:            "claude-code",
		Label:           "Claude Code",
		Description:     "Echte Claude-Code-Sandbox (claude -p, headless). Braucht ein Anthropic-Credential unter Secrets.",
		NeedsCredential: true,
		New:             func() Runtime { return NewClaudeCode() },
		Setup: []SetupStep{
			{
				Text: "Credential besorgen — eine der beiden Varianten:",
				Items: []string{
					"Abo (Pro/Max): im Terminal `claude setup-token` ausführen → Token mit Präfix `sk-ant-oat…`. Läuft über dein Abo-Kontingent, keine extra Abrechnung.",
					"API (pay-per-token): API-Key `sk-ant-api…` aus der Anthropic-Console. Wird separat abgerechnet.",
				},
			},
			{
				Text: "Unter `Secrets` hinterlegen — Key je nach Variante:",
				Items: []string{
					"Abo-Token → Key `claude_code_oauth_token`",
					"API-Key → Key `anthropic_api_key`",
				},
			},
			{Text: "Hier beim Agenten die Runtime auf `claude-code` stellen."},
			{Text: "Aufgabe ins Backlog stellen oder den Agenten `Wecken` — die Control Plane brokert das Credential zur Laufzeit kurzlebig in die Sandbox."},
		},
	})
}

func (c *ClaudeCode) Name() string { return "claude-code" }

// streamEvent ist der generische Blick auf eine stream-json-Zeile.
type streamEvent struct {
	Type         string   `json:"type"`
	Subtype      string   `json:"subtype"`
	SessionID    string   `json:"session_id"`
	Result       string   `json:"result"`
	IsError      bool     `json:"is_error"`
	Errors       []string `json:"errors"`
	NumTurns     int      `json:"num_turns"`
	TotalCostUSD float64  `json:"total_cost_usd"`
	// TerminalReason nennt den Abbruchgrund des Laufs ("completed",
	// "max_turns", "api_error", …). Für das Turn-Limit ist er die verlässlichere
	// Quelle als subtype, weil dort nur error_max_turns ankommt.
	TerminalReason string `json:"terminal_reason"`
	Usage          struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
}

// outcome ist das Roh-Ergebnis eines einzelnen `claude`-Prozesses, bevor es auf
// RunResult abgebildet wird.
type outcome struct {
	sessionID    string
	resultText   string
	subtype      string
	terminal     string
	numTurns     int
	isError      bool
	errors       []string
	costUSD      float64
	inputTokens  int64
	outputTokens int64
	sawResult    bool
	waitErr      error
}

// truncated erkennt den Lauf, der am Turn-Limit endete: Claude Code liefert
// dann subtype=error_max_turns bzw. terminal_reason=max_turns — und, anders als
// bei jedem anderen Fehler, **kein** result-Feld. Ohne Sonderbehandlung landet
// so ein Lauf als „failed" ohne Fehlertext und ohne Zwischenstand im Backlog.
func (o outcome) truncated() bool {
	return o.subtype == "error_max_turns" || o.terminal == "max_turns"
}

// maxTurnsSummaryPrompt holt den Zwischenstand aus der abgebrochenen Session.
// Bewusst knapp und ohne Werkzeuge: ein Turn auf dem bereits gecachten Kontext
// ist im Vergleich zum verlorenen Lauf praktisch gratis.
const maxTurnsSummaryPrompt = `Dein Lauf wurde am Turn-Limit abgebrochen — du bist NICHT fertig.
Arbeite jetzt nicht weiter und rufe keine Werkzeuge auf. Antworte nur mit einem
kurzen Übergabe-Stand (max. 15 Zeilen, Markdown) in genau dieser Gliederung:

## Erledigt
## Offen
## Nächster Schritt

Schreibe ihn so, dass ein Kollege ohne deinen Kontext daran weiterarbeiten kann:
konkrete Namen (Issue, MR, Branch, Datei, Pfad), keine Andeutungen.`

// maxTurnsSummaryTimeout deckelt den Zusammenfassungs-Lauf. Er ist ein einziger
// Turn ohne Werkzeuge; braucht er länger, ist etwas kaputt und die Aufgabe soll
// lieber ohne Übergabe scheitern, als die Sandbox zu belegen.
const maxTurnsSummaryTimeout = 2 * time.Minute

func (c *ClaudeCode) buildArgs(spec RunSpec) ([]string, string) {
	prompt := spec.Title + "\n\n" + spec.Body
	if spec.ResumeSessionID != "" {
		// Wiederaufnahme: Claude Code stellt den Kontext selbst wieder her,
		// wir geben nur das korrelierten Event als neue Eingabe hinein.
		prompt = spec.ResumeInput
		if prompt == "" {
			prompt = "Das Ereignis, auf das du gewartet hast, ist eingetreten. Setze die Aufgabe fort."
		}
	}
	args := []string{"-p", prompt, "--output-format", "stream-json", "--verbose",
		"--dangerously-skip-permissions"}
	if spec.ResumeSessionID != "" {
		args = append(args, "--resume", spec.ResumeSessionID)
	}
	systemPrompt := spec.SystemPrompt
	if spec.MemoryContext != "" {
		systemPrompt += "\n\n" + spec.MemoryContext
	}
	if systemPrompt != "" {
		args = append(args, "--append-system-prompt", systemPrompt)
	}
	if len(spec.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(spec.AllowedTools, ","))
	}
	if spec.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(spec.MaxTurns))
	}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	return args, prompt
}

// stream startet einen `claude`-Prozess, reicht jede stream-json-Zeile 1:1 ins
// Recording weiter und sammelt daraus das Roh-Ergebnis ein.
func (c *ClaudeCode) stream(ctx context.Context, spec RunSpec, args []string, onEvent func(kind string, payload json.RawMessage)) (outcome, error) {
	cmd := exec.CommandContext(ctx, c.Binary, args...)
	// Arbeitsverzeichnis und Home sind getrennt: Claude Code sucht CLAUDE.md,
	// .claude/agents, skills und commands relativ zum cwd — ein Sub-Run im
	// Projekt-Checkout bekommt so dessen Harness, behält aber das Agenten-Home.
	cmd.Dir = spec.WorkDir
	if cmd.Dir == "" {
		cmd.Dir = spec.HomeDir
	}
	cmd.Env = append(os.Environ(), spec.Env...)
	cmd.Env = append(cmd.Env, "HOME="+spec.HomeDir)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return outcome{}, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return outcome{}, fmt.Errorf("claude starten: %w", err)
	}

	var out outcome
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		// Jede stream-json-Zeile geht 1:1 als Recording-Event raus.
		raw := json.RawMessage(append([]byte(nil), line...))
		onEvent("runtime", raw)

		var ev streamEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue // Nicht-JSON-Zeilen tolerieren, Recording hat sie trotzdem
		}
		if ev.SessionID != "" {
			out.sessionID = ev.SessionID
		}
		if ev.Type == "result" {
			out.sawResult = true
			out.resultText = ev.Result
			out.subtype = ev.Subtype
			out.terminal = ev.TerminalReason
			out.numTurns = ev.NumTurns
			out.isError = ev.IsError
			out.errors = ev.Errors
			out.costUSD = ev.TotalCostUSD
			out.inputTokens = ev.Usage.InputTokens
			out.outputTokens = ev.Usage.OutputTokens
		}
	}
	out.waitErr = cmd.Wait()
	if scanErr := scanner.Err(); scanErr != nil && out.waitErr == nil {
		out.waitErr = scanErr
	}
	return out, nil
}

func (c *ClaudeCode) Run(ctx context.Context, spec RunSpec, onEvent func(kind string, payload json.RawMessage)) (RunResult, error) {
	args, _ := c.buildArgs(spec)
	out, err := c.stream(ctx, spec, args, onEvent)
	if err != nil {
		return RunResult{}, err
	}

	res := RunResult{
		SessionID:    out.sessionID,
		CostUSD:      out.costUSD,
		InputTokens:  out.inputTokens,
		OutputTokens: out.outputTokens,
	}
	resultText := out.resultText
	if out.isError {
		res.Status = "failed"
		// Nicht jeder Fehler-Subtype füllt result — dann trägt errors[] den Text.
		res.Error = out.resultText
		if res.Error == "" {
			res.Error = strings.Join(out.errors, "; ")
		}
	}
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	// Turn-Limit: kein Ergebnis, aber ein halb fertiger Lauf. Statt ihn
	// wortlos scheitern zu lassen, holen wir den Übergabe-Stand aus der Session
	// und melden ihn als "incomplete" — die Control Plane macht daraus eine
	// Folgeaufgabe, die genau hier weiterarbeitet (spec/03).
	if out.truncated() {
		res.Status = "incomplete"
		res.Error = fmt.Sprintf("Turn-Limit erreicht (%d Turns) — Lauf abgebrochen, bevor er zu einem Ergebnis kam", out.numTurns)
		res.Result = c.summarize(ctx, spec, out.sessionID, onEvent, &res)
		return res, nil
	}
	if out.waitErr != nil && !out.sawResult {
		// Exit ≠ 0 ohne result-Event: harter Fehlerpfad (spec/12).
		res.Status = "failed"
		res.Error = fmt.Sprintf("claude exit: %v", out.waitErr)
		return res, nil
	}
	if res.Status == "failed" {
		// "Not logged in · Please run /login" heißt: kein Credential in der
		// Sandbox angekommen. Ohne Übersetzung liest sich das wie ein
		// Covey-Login-Problem — deshalb hier der actionable Hinweis.
		if strings.Contains(res.Error, "/login") {
			res.Error = fmt.Sprintf(
				"Claude Code hat kein Credential in der Sandbox (%q). "+
					"In Covey unter Secrets `anthropic_api_key` (API-Key) oder "+
					"`claude_code_oauth_token` (Abo, via `claude setup-token`) hinterlegen.",
				res.Error)
		}
		// "Invalid bearer token" (401): das gebrokerte Abo-Token ist abgelaufen
		// oder widerrufen — ohne Übersetzung klingt das nach einem Covey-Bug.
		if strings.Contains(res.Error, "Invalid bearer token") ||
			strings.Contains(res.Error, "OAuth token has expired") {
			res.Error = fmt.Sprintf(
				"Das hinterlegte Abo-Token wird von der Anthropic-API abgelehnt (%q). "+
					"Abo-Token laufen ab bzw. werden bei erneutem Login widerrufen — im Terminal "+
					"`claude setup-token` ausführen und das neue Token in Covey unter Secrets als "+
					"`claude_code_oauth_token` speichern.",
				res.Error)
		}
		return res, nil
	}
	applyStatus(&res, resultText)
	return res, nil
}

// summarize fragt die am Turn-Limit abgebrochene Session nach ihrem
// Übergabe-Stand: ein einziger Turn per --resume auf dem bereits gecachten
// Kontext, ohne Werkzeuge. Das Ergebnis wird der Zwischenstand der Aufgabe und
// der Auftragstext ihrer Folgeaufgabe.
//
// Best-effort: scheitert der Zusatzlauf (keine Session-ID, Timeout, Fehler),
// bleibt es beim Fehlertext des Hauptlaufs — die Folgeaufgabe entsteht dann
// ohne Übergabe, aber sie entsteht. Die Kosten des Zusatzlaufs werden auf das
// Ergebnis addiert, damit die Abrechnung vollständig bleibt.
func (c *ClaudeCode) summarize(ctx context.Context, spec RunSpec, sessionID string, onEvent func(kind string, payload json.RawMessage), res *RunResult) string {
	if sessionID == "" {
		return ""
	}
	args := []string{"-p", maxTurnsSummaryPrompt, "--output-format", "stream-json", "--verbose",
		"--dangerously-skip-permissions", "--resume", sessionID, "--max-turns", "1"}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	sctx, cancel := context.WithTimeout(ctx, maxTurnsSummaryTimeout)
	defer cancel()

	out, err := c.stream(sctx, spec, args, onEvent)
	res.CostUSD += out.costUSD
	res.InputTokens += out.inputTokens
	res.OutputTokens += out.outputTokens
	if err != nil || out.isError {
		return ""
	}
	return strings.TrimSpace(out.resultText)
}
