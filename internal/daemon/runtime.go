package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// DefaultAllowedTools is the default tool scope of a run as long as no agent
// config says otherwise: read/write/search files, shell, fetch and search web
// pages, structure tasks. The hard boundary stays outside the runtime (broker,
// egress, guard-rails) — this list is the soft inner boundary from spec/12.
var DefaultAllowedTools = []string{
	"Bash", "BashOutput", "KillShell",
	"Read", "Write", "Edit", "Glob", "Grep", "NotebookEdit",
	"WebFetch", "WebSearch",
	"Task", "TodoWrite",
}

// RunSpec is everything a runtime adapter needs for one run.
type RunSpec struct {
	TaskID          string
	Title           string
	Body            string
	SystemPrompt    string
	MemoryContext   string
	Model           string // desired LLM; empty = the runtime's default
	AllowedTools    []string
	MaxTurns        int
	MaxBudgetUSD    float64
	ResumeSessionID string
	ResumeInput     string
	HomeDir         string
	// WorkDir is the run's working directory. Empty = HomeDir. Kept separate
	// from the home so a sub-run can start INSIDE the project checkout (where
	// the project's own Claude Code harness applies: CLAUDE.md, .claude/agents,
	// skills), while HOME still points at the persistent agent home — that is
	// where ~/.claude, the wiki working copy and the dependency caches live.
	WorkDir string
	Env     []string // extra ENV (e.g. COVEY_ACTION_PORT, brokered keys)
}

// RunResult is the normalized result of a runtime run.
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

// Runtime is the adapter port (spec/01): thin, translating between the daemon
// protocol and the specifics of the respective runtime.
type Runtime interface {
	Name() string
	Run(ctx context.Context, spec RunSpec, onEvent func(kind string, payload json.RawMessage)) (RunResult, error)
}

// childEnv builds a subprocess's environment out of the daemon's environment —
// WITHOUT the daemon's own COVEY_* variables. The reason is the route to the
// broker: COVEY_WS_URL and COVEY_DAEMON_TOKEN are credentials for the control
// plane with which a child process could open its own WebSocket and send
// `request_credential` — reaching exactly the brokered accesses that omitting
// COVEY_ACTION_PORT keeps out of reach.
//
// For the outer run that would be inconsequential (it executes the agent's
// compiled config); for a sub-run it would not: there, repository content is
// executable configuration (hooks, MCP servers) — so the filter applies to every
// subprocess. Whatever a run legitimately needs comes in explicitly via extra
// (COVEY_ACTION_PORT, brokered LLM key), not by inheritance.
func childEnv(extra ...string) []string {
	host := os.Environ()
	env := make([]string, 0, len(host)+len(extra))
	for _, kv := range host {
		if strings.HasPrefix(kv, "COVEY_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

// covey_status is the closing line the compiled config demands from the runtime
// (see agents.ProtocolInstructions).
type covey_status struct {
	Status         string `json:"status"`
	Result         string `json:"result"`
	CorrelationKey string `json:"correlation_key"`
	Question       string `json:"question"`
	Memory         string `json:"memory"`
}

const statusMarker = "COVEY_STATUS:"

// ParseStatusLine looks for the last COVEY_STATUS line in the output.
// If it is missing, the run counts as done with the whole text as its result —
// fail-open would be wrong for blocked, but done without a marker is the
// forgiving default for trivial tasks.
func ParseStatusLine(output string) (covey_status, bool) {
	idx := strings.LastIndex(output, statusMarker)
	if idx < 0 {
		return covey_status{}, false
	}
	rest := strings.TrimSpace(output[idx+len(statusMarker):])
	// Only the JSON line, not any text that may follow.
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

// applyStatus transfers the COVEY_STATUS line into the RunResult.
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
			// blocked without a key can never be woken → treat it as failed.
			res.Status = "failed"
			res.Error = "runtime reported blocked without a correlation_key"
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
		res.Error = fmt.Sprintf("unknown COVEY_STATUS %q", st.Status)
	}
}
