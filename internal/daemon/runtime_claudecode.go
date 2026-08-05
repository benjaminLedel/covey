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

// ClaudeCode drives Claude Code headless through `claude -p` (spec/12): prompt
// in, stream-json events out, `--resume <session_id>` for the blocked→working
// edge.
type ClaudeCode struct {
	// Binary is the CLI path; default "claude", overridable via the ENV var
	// COVEY_CLAUDE_BIN (that is how we test the adapter against a fake binary).
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
		Description:     "Real Claude Code sandbox (claude -p, headless). Needs an Anthropic credential under Secrets.",
		NeedsCredential: true,
		New:             func() Runtime { return NewClaudeCode() },
		Setup: []SetupStep{
			{
				Text: "Obtain a credential — one of the two variants:",
				Items: []string{
					"Subscription (Pro/Max): run `claude setup-token` in a terminal → token with the prefix `sk-ant-oat…`. Uses your subscription quota, no separate billing.",
					"API (pay-per-token): API key `sk-ant-api…` from the Anthropic console. Billed separately.",
				},
			},
			{
				Text: "Store it under `Secrets` — key depending on the variant:",
				Items: []string{
					"Subscription token → key `claude_code_oauth_token`",
					"API key → key `anthropic_api_key`",
				},
			},
			{Text: "Set this agent's runtime to `claude-code`."},
			{Text: "Put a task into the backlog or `Wake` the agent — the control plane brokers the credential into the sandbox at runtime, short-lived."},
		},
	})
}

func (c *ClaudeCode) Name() string { return "claude-code" }

// streamEvent is the generic view of a stream-json line.
type streamEvent struct {
	Type         string   `json:"type"`
	Subtype      string   `json:"subtype"`
	SessionID    string   `json:"session_id"`
	Result       string   `json:"result"`
	IsError      bool     `json:"is_error"`
	Errors       []string `json:"errors"`
	NumTurns     int      `json:"num_turns"`
	TotalCostUSD float64  `json:"total_cost_usd"`
	// TerminalReason names why the run ended ("completed", "max_turns",
	// "api_error", …). For the turn limit it is the more reliable source than
	// subtype, because there only error_max_turns arrives.
	TerminalReason string `json:"terminal_reason"`
	// Usage: input_tokens counts ONLY the uncached input. With Claude Code
	// practically the entire context comes out of the prompt cache, so that
	// field stays in the low three digits while the run reads millions of
	// tokens. Without the two cache fields the input side of the billing is
	// off by three orders of magnitude — measured on a single run: 56
	// input_tokens against 2,341,568 cache_read_input_tokens.
	Usage struct {
		InputTokens         int64 `json:"input_tokens"`
		OutputTokens        int64 `json:"output_tokens"`
		CacheReadTokens     int64 `json:"cache_read_input_tokens"`
		CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	// Message carries the model on the assistant events. The result event does
	// NOT name it, which is why cost entries used to end up with an empty model
	// and the cost breakdown reported everything as "unknown".
	Message struct {
		Model string `json:"model"`
	} `json:"message"`
}

// outcome is the raw result of a single `claude` process, before it is mapped
// onto a RunResult.
type outcome struct {
	sessionID           string
	resultText          string
	subtype             string
	terminal            string
	model               string
	numTurns            int
	isError             bool
	errors              []string
	costUSD             float64
	inputTokens         int64
	outputTokens        int64
	cacheReadTokens     int64
	cacheCreationTokens int64
	sawResult           bool
	waitErr             error
}

// truncated recognizes the run that ended at the turn limit: Claude Code then
// reports subtype=error_max_turns or terminal_reason=max_turns — and, unlike
// with any other error, **no** result field. Without special handling such a
// run lands in the backlog as "failed", with no error text and no interim state.
func (o outcome) truncated() bool {
	return o.subtype == "error_max_turns" || o.terminal == "max_turns"
}

// maxTurnsSummaryPrompt fetches the interim state from the aborted session.
// Deliberately terse and without tools: one turn against the already cached
// context, and cheap compared to the run that would otherwise be lost — but not
// free. Measured on a real handover: 0.63 USD for that single turn, because
// --resume rewrites the cache (58,843 cache-creation tokens). That is the price
// of the follow-up task, and it belongs in the billing, hence res is updated in
// summarize().
const maxTurnsSummaryPrompt = `Your run was cut off at the turn limit — you are NOT finished.
Do not continue working now and do not call any tools. Answer only with a short
handover state (max. 15 lines, Markdown) in exactly this structure:

## Done
## Open
## Next step

Write it so that a colleague without your context can carry on from it: concrete
names (issue, MR, branch, file, path), no hints.`

// maxTurnsSummaryTimeout caps the summary run. It is a single turn without
// tools; if it takes longer, something is broken and the task should rather
// fail without a handover than occupy the sandbox.
const maxTurnsSummaryTimeout = 2 * time.Minute

func (c *ClaudeCode) buildArgs(spec RunSpec) ([]string, string) {
	prompt := spec.Title + "\n\n" + spec.Body
	if spec.ResumeSessionID != "" {
		// Resumption: Claude Code restores the context itself, we only feed in
		// the correlated event as the new input.
		prompt = spec.ResumeInput
		if prompt == "" {
			prompt = "The event you were waiting for has occurred. Continue the task."
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

// stream starts a `claude` process, passes every stream-json line 1:1 into the
// recording and collects the raw result from it.
func (c *ClaudeCode) stream(ctx context.Context, spec RunSpec, args []string, onEvent func(kind string, payload json.RawMessage)) (outcome, error) {
	cmd := exec.CommandContext(ctx, c.Binary, args...)
	// Working directory and home are separate: Claude Code looks for CLAUDE.md,
	// .claude/agents, skills and commands relative to the cwd — that way a
	// sub-run in the project checkout gets its harness while keeping the agent
	// home.
	cmd.Dir = spec.WorkDir
	if cmd.Dir == "" {
		cmd.Dir = spec.HomeDir
	}
	// Environment without the daemon's COVEY_* variables (see childEnv): the run
	// gets only what the caller explicitly hands it — otherwise a hook or MCP
	// server from the project would inherit the daemon token.
	cmd.Env = childEnv(spec.Env...)
	cmd.Env = append(cmd.Env, "HOME="+spec.HomeDir)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return outcome{}, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return outcome{}, fmt.Errorf("starting claude: %w", err)
	}

	var out outcome
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		// Every stream-json line goes out 1:1 as a recording event.
		raw := json.RawMessage(append([]byte(nil), line...))
		onEvent("runtime", raw)

		var ev streamEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue // tolerate non-JSON lines, the recording has them anyway
		}
		if ev.SessionID != "" {
			out.sessionID = ev.SessionID
		}
		// The model is only named on the way past (system/init and every
		// assistant event), never on the result. Whoever wants it has to pick it
		// up here.
		if ev.Message.Model != "" {
			out.model = ev.Message.Model
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
			out.cacheReadTokens = ev.Usage.CacheReadTokens
			out.cacheCreationTokens = ev.Usage.CacheCreationTokens
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
		SessionID:           out.sessionID,
		CostUSD:             out.costUSD,
		InputTokens:         out.inputTokens,
		OutputTokens:        out.outputTokens,
		CacheReadTokens:     out.cacheReadTokens,
		CacheCreationTokens: out.cacheCreationTokens,
		Model:               out.model,
	}
	resultText := out.resultText
	if out.isError {
		res.Status = "failed"
		// Not every error subtype fills result — then errors[] carries the text.
		res.Error = out.resultText
		if res.Error == "" {
			res.Error = strings.Join(out.errors, "; ")
		}
	}
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	// Turn limit: no result, but a half-finished run. Instead of letting it fail
	// silently we fetch the handover state from the session and report it as
	// "incomplete" — the control plane turns that into a follow-up task that
	// carries on exactly here (spec/03).
	if out.truncated() {
		res.Status = "incomplete"
		res.Error = fmt.Sprintf("turn limit reached (%d turns) — run cut off before it produced a result", out.numTurns)
		res.Result = c.summarize(ctx, spec, out.sessionID, onEvent, &res)
		return res, nil
	}
	if out.waitErr != nil && !out.sawResult {
		// Exit ≠ 0 without a result event: the hard failure path (spec/12).
		res.Status = "failed"
		res.Error = fmt.Sprintf("claude exit: %v", out.waitErr)
		return res, nil
	}
	if res.Status == "failed" {
		// "Not logged in · Please run /login" means: no credential arrived in the
		// sandbox. Untranslated this reads like a Covey login problem — hence the
		// actionable hint here.
		if strings.Contains(res.Error, "/login") {
			res.Error = fmt.Sprintf(
				"Claude Code has no credential in the sandbox (%q). "+
					"Store `anthropic_api_key` (API key) or `claude_code_oauth_token` "+
					"(subscription, via `claude setup-token`) in Covey under Secrets.",
				res.Error)
		}
		// "Invalid bearer token" (401): the brokered subscription token has
		// expired or been revoked — untranslated that sounds like a Covey bug.
		if strings.Contains(res.Error, "Invalid bearer token") ||
			strings.Contains(res.Error, "OAuth token has expired") {
			res.Error = fmt.Sprintf(
				"The stored subscription token is rejected by the Anthropic API (%q). "+
					"Subscription tokens expire and are revoked on a new login — run "+
					"`claude setup-token` in a terminal and save the new token in Covey under "+
					"Secrets as `claude_code_oauth_token`.",
				res.Error)
		}
		return res, nil
	}
	applyStatus(&res, resultText)
	return res, nil
}

// summarize asks the session cut off at the turn limit for its handover state:
// a single turn via --resume on the already cached context, without tools. The
// result becomes the task's interim state and the brief of its follow-up task.
//
// Best-effort: if the extra run fails (no session ID, timeout, error), the main
// run's error text stands — the follow-up task is then created without a
// handover, but it is created. The cost of the extra run is added to the result
// so that billing stays complete.
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
	res.CacheReadTokens += out.cacheReadTokens
	res.CacheCreationTokens += out.cacheCreationTokens
	if res.Model == "" {
		res.Model = out.model
	}
	if err != nil || out.isError {
		return ""
	}
	return strings.TrimSpace(out.resultText)
}
