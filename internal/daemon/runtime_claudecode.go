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
	Type         string  `json:"type"`
	Subtype      string  `json:"subtype"`
	SessionID    string  `json:"session_id"`
	Result       string  `json:"result"`
	IsError      bool    `json:"is_error"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	Usage        struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
}

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
	return args, prompt
}

func (c *ClaudeCode) Run(ctx context.Context, spec RunSpec, onEvent func(kind string, payload json.RawMessage)) (RunResult, error) {
	args, _ := c.buildArgs(spec)
	cmd := exec.CommandContext(ctx, c.Binary, args...)
	cmd.Dir = spec.HomeDir
	cmd.Env = append(os.Environ(), spec.Env...)
	cmd.Env = append(cmd.Env, "HOME="+spec.HomeDir)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return RunResult{}, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return RunResult{}, fmt.Errorf("claude starten: %w", err)
	}

	var res RunResult
	var resultText string
	sawResult := false
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
			res.SessionID = ev.SessionID
		}
		if ev.Type == "result" {
			sawResult = true
			resultText = ev.Result
			res.CostUSD = ev.TotalCostUSD
			res.InputTokens = ev.Usage.InputTokens
			res.OutputTokens = ev.Usage.OutputTokens
			if ev.IsError {
				res.Status = "failed"
				res.Error = ev.Result
			}
		}
	}
	waitErr := cmd.Wait()
	if scanErr := scanner.Err(); scanErr != nil && waitErr == nil {
		waitErr = scanErr
	}
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	if waitErr != nil && !sawResult {
		// Exit ≠ 0 ohne result-Event: harter Fehlerpfad (spec/12).
		res.Status = "failed"
		res.Error = fmt.Sprintf("claude exit: %v", waitErr)
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
