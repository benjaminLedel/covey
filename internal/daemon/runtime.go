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
//
// The list is at the same time the LOADING scope of the run: it is passed to
// Claude Code both as --allowedTools (permission) and as --tools (which
// built-in tools exist at all). That distinction is the whole point —
// --allowedTools leaves every built-in tool's schema in the context and only
// gates its use, --tools keeps it out. Measured on an idle run: the runtime's
// full built-in set costs 20.811 prompt tokens, this list 11.045. The prefix is
// read afresh on EVERY turn, so a run of 70 turns saves some 680.000 cache-read
// tokens by it.
//
// Deliberately WITHOUT "Skill": the tool pulls the descriptions of all built-in
// skills into the prompt (+2.454 tokens, measured) and an agent needs them only
// if skills have been materialized for it. buildArgs adds the entry for exactly
// that run (RunSpec.Skills).
var DefaultAllowedTools = []string{
	"Bash", "BashOutput", "KillShell",
	"Read", "Write", "Edit", "Glob", "Grep", "NotebookEdit",
	"WebFetch", "WebSearch",
	"Task", "TodoWrite",
}

// skillTool is the runtime's built-in tool through which materialized skills
// (skillslocal.go) become reachable. Without it in --tools Claude Code loads no
// skill at all — including the agent's own ones.
const skillTool = "Skill"

// mcpToolPrefix marks the tools of an MCP server (the action proxy). They are
// not part of the built-in set and must therefore NOT land in --tools — the
// flag knows only built-in names, an MCP entry there would be silently dropped
// while the tool disappears from the permission list.
const mcpToolPrefix = "mcp__"

// BuiltinTools filters a tool scope down to the built-in names --tools accepts.
// With skills the Skill tool joins them, otherwise it deliberately stays out
// (see DefaultAllowedTools). The result is nil if the scope contains no
// built-in name — the caller then leaves the flag off rather than passing an
// empty list, which Claude Code reads as "no tools at all".
func BuiltinTools(scope []string, withSkills bool) []string {
	out := make([]string, 0, len(scope)+1)
	hasSkill := false
	for _, t := range scope {
		if strings.HasPrefix(t, mcpToolPrefix) {
			continue
		}
		if t == skillTool {
			hasSkill = true
		}
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil
	}
	if withSkills && !hasSkill {
		out = append(out, skillTool)
	}
	return out
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
	// Skills says whether skills were materialized into the home for this run
	// (skillslocal.go). Only then does the Skill tool belong in the loading
	// scope — otherwise it costs prompt tokens for skills the agent does not
	// have.
	Skills   bool
	MaxTurns int
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
	// MCPConfig is the MCP server document the runtime is started with — the
	// action proxy, so target actions arrive as typed tools instead of as a curl
	// in the shell (actionmcp.go). Empty = without, the shell route as before.
	MCPConfig string
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
	// CacheReadTokens/CacheCreationTokens are the input side that actually
	// carries the weight: with a cached context InputTokens counts only the few
	// uncached tokens. Kept separate rather than added into InputTokens, because
	// the three are priced differently — a cache read costs a tenth of fresh
	// input, writing the cache costs a quarter more.
	CacheReadTokens     int64
	CacheCreationTokens int64
	Model               string
}

// TotalInputTokens is the input side as a human means it: everything the model
// read this run, cached or not.
func (r RunResult) TotalInputTokens() int64 {
	return r.InputTokens + r.CacheReadTokens + r.CacheCreationTokens
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
