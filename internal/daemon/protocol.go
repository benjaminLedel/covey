// Package daemon holds the bidirectional daemon protocol (spec/01) and the
// daemon side of it (runtime adapter, action proxy). The protocol is the
// system's stable seam: runtimes change, the protocol stays.
package daemon

import (
	"encoding/json"
	"fmt"
)

// Message is the envelope for both directions (JSON over WebSocket).
type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Control plane → daemon. ("wake" is not a WS frame: waking = the control
// plane starts the sandbox through the SandboxProvider; the daemon then
// connects and reports ready.)
const (
	TypeInjectConfig = "inject_config"
	TypeAssignTask   = "assign_task"
	// #nosec G101 — the name of a message kind, not a credential.
	TypeInjectCredentials = "inject_credentials"
	TypeApprovalDecision  = "approval_decision"
	TypeInjectTarget      = "inject_target"
	TypeInjectOrgChart    = "inject_org_chart"
	TypeInjectWiki        = "inject_wiki"
	TypeInjectSkills      = "inject_skills"
	// #nosec G101 — the name of a message kind, not a secret.
	TypeInjectSecret = "inject_secret"
	// TypeRequestUsage/TypeUsageReport: the control plane asks the daemon what
	// the credential in effect has consumed (spec/18). It runs in the sandbox
	// because that is where the credential is; the control plane receives a
	// figure, not a binary.
	TypeRequestUsage = "request_usage"
	TypeUsageReport  = "usage_report"
	TypeKill         = "kill"
	TypeSleep        = "sleep"
)

// Daemon → control plane.
const (
	TypeReady             = "ready"
	TypeEvent             = "event"
	TypeRequestCredential = "request_credential"
	TypeRequestApproval   = "request_approval"
	TypeRequestTarget     = "request_target"
	TypeRequestOrgChart   = "request_org_chart"
	TypeBlocked           = "blocked"
	TypeTaskDone          = "task_done"
	TypeCost              = "cost"
	TypeHeartbeat         = "heartbeat"
	TypeSetStage          = "set_stage"
	TypeNote              = "note"
	TypeRequestWiki       = "request_wiki"
	TypeRequestSkills     = "request_skills"
	TypeRequestCreateTask = "request_create_task"
	TypeRequestHiring     = "request_hiring"
	// #nosec G101 — the name of a message kind, not a secret.
	TypeRequestSecret = "request_secret"
)

// Control plane → daemon (answers to request_create_task / request_hiring).
const (
	TypeInjectCreateTask = "inject_create_task"
	TypeInjectHiring     = "inject_hiring"
)

type InjectConfig struct {
	SystemPrompt string   `json:"system_prompt"`
	Runtime      string   `json:"runtime"`
	Model        string   `json:"model,omitempty"` // empty = runtime default
	AllowedTools []string `json:"allowed_tools,omitempty"`
	MaxTurns     int      `json:"max_turns,omitempty"`
	MaxBudgetUSD float64  `json:"max_budget_usd,omitempty"`
	// ActionTools are the target systems this agent may use, in the wording of
	// its prompt. The action proxy serves them as MCP tools, so a runtime that
	// speaks MCP can call an action directly instead of assembling a curl by
	// hand in the shell. An older daemon does not know the field and keeps to
	// the curl route — both paths stay open.
	ActionTools []ActionTool `json:"action_tools,omitempty"`
}

// ActionTool is one target system as a callable tool: the name the action proxy
// listens on and the description the runtime shows the model. The description
// is the plugin's PromptDoc — the same text that otherwise stands in the system
// prompt, so there is one wording, not two.
type ActionTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type AssignTask struct {
	TaskID          string `json:"task_id"`
	Title           string `json:"title"`
	Body            string `json:"body"`
	Priority        int    `json:"priority"`
	MemoryContext   string `json:"memory_context,omitempty"`
	ResumeSessionID string `json:"resume_session_id,omitempty"`
	ResumeInput     string `json:"resume_input,omitempty"`
}

type InjectCredentials struct {
	RequestID string `json:"request_id"`
	System    string `json:"system"`
	Granted   bool   `json:"granted"`
	Reason    string `json:"reason,omitempty"`
	Token     string `json:"token,omitempty"`
	BaseURL   string `json:"base_url,omitempty"`
	// Path delivers the value as a FILE at this path in the agent home instead
	// of as an environment variable — the form some engines require for their
	// subscription login (spec/19). Written for the run, removed after it.
	Path    string `json:"path,omitempty"`
	TTLSecs int    `json:"ttl_secs,omitempty"`
	// EnvVar names the runtime's target environment variable when the control
	// plane knows the credential type (from the secret's name). If it is empty,
	// the daemon guesses from the token prefix (backwards compatibility).
	EnvVar string `json:"env_var,omitempty"`
}

// ApprovalDecision is the central policy decision on an action: approved
// (possibly auto-allow), denied (guard-rail) or pending (waits for human
// approval; correlation_key wakes the task once the decision is made).
type ApprovalDecision struct {
	RequestID      string `json:"request_id"`
	Status         string `json:"status"` // approved | denied | pending
	Reason         string `json:"reason,omitempty"`
	ApprovalID     string `json:"approval_id,omitempty"`
	CorrelationKey string `json:"correlation_key,omitempty"`
}

type Ready struct {
	AgentID string `json:"agent_id"`
}

type Event struct {
	TaskID  string          `json:"task_id,omitempty"`
	Kind    string          `json:"kind"` // runtime | action | http | tool_use ...
	Payload json.RawMessage `json:"payload"`
}

// Event kinds with special handling in the control plane.
const (
	// EventKindAction is an executed target-system action (→ recording).
	EventKindAction = "action"
	// EventKindHTTP is a single HTTP request a target-system plugin made inside
	// the sandbox (payload: reqlog.Entry). It does not go into the recording but
	// into the request log — diagnostics, not an audit trail.
	EventKindHTTP = "http"
)

// RequestTarget/InjectTarget broker the definition of a manifest plugin
// (kind=custom) into the sandbox: the daemon knows compiled plugins itself,
// uploaded manifests it fetches from the control plane — only if the system is
// enabled for the organization (fail-closed).
type RequestTarget struct {
	RequestID string `json:"request_id"`
	System    string `json:"system"`
}

type InjectTarget struct {
	RequestID string `json:"request_id"`
	System    string `json:"system"`
	Granted   bool   `json:"granted"`
	Reason    string `json:"reason,omitempty"`
	// Kind tells apart the definition in Manifest: "custom" (REST manifest) or
	// "mcp" (MCP server config). Empty = custom (backwards compatibility).
	Kind     string          `json:"kind,omitempty"`
	Manifest json.RawMessage `json:"manifest,omitempty"`
}

// RequestOrgChart/InjectOrgChart broker the organization's org chart into the
// sandbox: humans and agents with their profiles (including the configurable
// profile fields), departments and reporting lines. A read-only view of the
// same data as GET /org/chart — the agent queries it at runtime through the
// meta action covey/org_chart instead of relying on a stale prompt snapshot.
type RequestOrgChart struct {
	RequestID string `json:"request_id"`
}

type InjectOrgChart struct {
	RequestID string          `json:"request_id"`
	Chart     json.RawMessage `json:"chart"`
}

type RequestCredential struct {
	RequestID string   `json:"request_id"`
	System    string   `json:"system"`
	Scopes    []string `json:"scopes,omitempty"`
	TaskID    string   `json:"task_id,omitempty"`
}

// RequestSecret/InjectSecret broker one custom, agent-scoped secret value
// (spec/04) for the {{secret:<key>}} placeholder in an action's params: the
// action proxy substitutes it server-side (in the sandbox, after the model
// has already committed to the tool call) so the plaintext value never enters
// the model's own context. Unlike RequestCredential/InjectCredentials this is
// not tied to a target system — the key is whatever name the secret was
// stored under (PUT /api/v1/agents/{id}/secrets/{key} or an org secret
// assigned to the agent). The explicit PutAgent/Assign onto this agent IS the
// authorization; there is no separate ACCESS.md scope for it.
type RequestSecret struct {
	RequestID string `json:"request_id"`
	Key       string `json:"key"`
	TaskID    string `json:"task_id,omitempty"`
}

// #nosec G101 — a message payload struct, not a hardcoded secret.
type InjectSecret struct {
	RequestID string `json:"request_id"`
	Key       string `json:"key"`
	Granted   bool   `json:"granted"`
	Reason    string `json:"reason,omitempty"`
	Value     string `json:"value,omitempty"`
}

type RequestApproval struct {
	RequestID string          `json:"request_id"`
	TaskID    string          `json:"task_id,omitempty"`
	Action    string          `json:"action"` // e.g. "zammad:reply_external"
	Params    json.RawMessage `json:"params"`
}

type Blocked struct {
	TaskID         string `json:"task_id"`
	CorrelationKey string `json:"correlation_key"`
	Question       string `json:"question,omitempty"`
	SessionID      string `json:"session_id,omitempty"` // runtime session for --resume
}

// TaskDone closes a task. Status "incomplete" is the special case of a run cut
// off at the turn limit: it did work but reached no result. Result then carries
// the handover state ("Done / Open / Next step") the adapter obtains from the
// aborted session, and SessionID the session to resume from. The control plane
// turns that into a follow-up task instead of a silent failure (see
// orchestrator.handleIncomplete).
type TaskDone struct {
	TaskID    string `json:"task_id"`
	Status    string `json:"status"` // done | failed | escalated | incomplete
	Result    string `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
	Memory    string `json:"memory,omitempty"` // episode for the memory (M7)
	SessionID string `json:"session_id,omitempty"`
}

// SetStage moves a task on the kanban board into a (possibly new) stage. Purely
// presentational — no lifecycle transition. If the stage is new, the control
// plane creates it ("the agent invents columns"). Empty TaskID = current task.
type SetStage struct {
	TaskID string `json:"task_id,omitempty"`
	Stage  string `json:"stage"`
}

// Note is a proactive note the agent takes mid-run. Scope "task" attaches it to
// the task (interim findings — task-specific), scope "memory" feeds it into the
// memory right away (generally valid insights) — without waiting for the memory
// field of task_done. Empty TaskID = current task.
type Note struct {
	TaskID  string `json:"task_id,omitempty"`
	Scope   string `json:"scope"` // task | memory
	Content string `json:"content"`
}

type Cost struct {
	TaskID       string  `json:"task_id,omitempty"`
	USD          float64 `json:"usd"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	// The cached input side, reported separately because it is priced
	// separately. A daemon of an older build does not send the two fields; they
	// then stay 0 and the entry looks the way it always did — no migration of
	// running sandboxes needed.
	CacheReadTokens     int64  `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int64  `json:"cache_creation_tokens,omitempty"`
	Model               string `json:"model,omitempty"`
}

// RequestWiki/InjectWiki broker the agent's wiki tools (covey/wiki_*) and the
// home working copy into the control plane: search (vector search across the
// pages), read (one page by slug), write (create/update a page, [[slug]]
// wikilinks in the body) and list (all pages, for materializing into the home).
// The wiki lives in the control plane (source of truth, spec/05).
type RequestWiki struct {
	RequestID string   `json:"request_id"`
	Op        string   `json:"op"` // search | read | write | append | delete | list
	Query     string   `json:"query,omitempty"`
	Slug      string   `json:"slug,omitempty"`
	Title     string   `json:"title,omitempty"`
	Body      string   `json:"body,omitempty"`
	Type      string   `json:"type,omitempty"` // entity type of the page (spec/05)
	Tags      []string `json:"tags,omitempty"`
	Text      string   `json:"text,omitempty"` // append only: the paragraph to add
}

type InjectWiki struct {
	RequestID string          `json:"request_id"`
	OK        bool            `json:"ok"`
	Error     string          `json:"error,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// RequestSkills/InjectSkills fetch an agent's skills for materializing into the
// home. Unlike the wiki there is no way back: skills are config from the
// control plane (library or agent-owned), the agent does not edit them — a run
// that writes itself new capabilities would not be a feature but the loss of
// central control.
type RequestSkills struct {
	RequestID string `json:"request_id"`
}

// SkillDir is a skill as a directory: name, description and the files relative
// to it. SKILL.md comes WITHOUT frontmatter — the daemon generates it while
// writing, from name and description, so both are maintained in one place only.
type SkillDir struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Files       map[string]string `json:"files"`
}

type InjectSkills struct {
	RequestID string     `json:"request_id"`
	OK        bool       `json:"ok"`
	Error     string     `json:"error,omitempty"`
	Skills    []SkillDir `json:"skills"`
}

// RequestCreateTask/InjectCreateTask are the meta action covey/create_task: the
// agent creates a task — either a **subtask** for itself (splitting work that
// is too large instead of running into the turn limit) or a **delegation** to a
// colleague (Agent = that colleague's slug). The new task hangs as a child off
// the running task; that is what the loop protection counts.
//
// Unlike the other covey meta actions it goes through the guard-rails (subject
// covey:create_task, or covey:create_task:foreign when delegating) — creating a
// task produces work and cost and must be governable.
type RequestCreateTask struct {
	RequestID string `json:"request_id"`
	TaskID    string `json:"task_id,omitempty"` // running task = parent
	Agent     string `json:"agent,omitempty"`   // target agent (slug); empty = myself
	Title     string `json:"title"`
	Body      string `json:"body"`
	Priority  int    `json:"priority,omitempty"`
}

type InjectCreateTask struct {
	RequestID string `json:"request_id"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	TaskID    string `json:"task_id,omitempty"` // the created task
	Agent     string `json:"agent,omitempty"`   // resolved target agent (slug)
}

// RequestHiring/InjectHiring are the meta actions with which an agent drafts
// another agent (covey/list_targets, get_agent_config, create_agent,
// set_agent_config — spec/20). The platform's own target system, so to speak:
// no external system, no credential, executed in the control plane.
//
// Like create_task and unlike the other meta actions they go through the
// guard-rails (subject covey:<op>) — what comes out of them is a colleague, and
// that must be governable centrally rather than in a prompt.
//
// What is deliberately NOT in here: hiring. An agent may draft another agent;
// employing one is a human act (spec/20). There is no op for it, so there is
// nothing to forget to check.
type RequestHiring struct {
	RequestID string `json:"request_id"`
	Op        string `json:"op"` // list_targets | get_agent_config | create_agent | set_agent_config
	TaskID    string `json:"task_id,omitempty"`
	// Agent addresses an existing agent by slug (get_agent_config,
	// set_agent_config).
	Agent string `json:"agent,omitempty"`
	// The new colleague (create_agent).
	Slug        string `json:"slug,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Runtime     string `json:"runtime,omitempty"`
	JobTitle    string `json:"job_title,omitempty"`
	Department  string `json:"department,omitempty"` // name, not ID — the model reads names
	Supervisor  string `json:"supervisor,omitempty"` // human's email or display name
	// Files is the config (set_agent_config): file name → complete content.
	Files map[string]string `json:"files,omitempty"`
}

// InjectHiring is the answer to a meta action. Pending is the third outcome
// next to OK and Error, and it is the reason this type is not just a boolean:
// a guard rail may put a meta action in front of a human (spec/21). The action
// has then NOT happened — the agent blocks on CorrelationKey and repeats it
// once somebody has decided, exactly as with a target-system action.
type InjectHiring struct {
	RequestID string          `json:"request_id"`
	OK        bool            `json:"ok"`
	Error     string          `json:"error,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`

	Pending        bool   `json:"pending,omitempty"`
	ApprovalID     string `json:"approval_id,omitempty"`
	CorrelationKey string `json:"correlation_key,omitempty"`
}

// Encode builds an envelope; never panics, because all payloads are marshalable.
func Encode(msgType string, payload any) (Message, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Message{}, fmt.Errorf("encode %s: %w", msgType, err)
	}
	return Message{Type: msgType, Payload: raw}, nil
}

// DecodePayload unpacks an envelope's payload in a typed way.
func DecodePayload[T any](m Message) (T, error) {
	var v T
	if len(m.Payload) == 0 {
		return v, fmt.Errorf("%s: empty payload", m.Type)
	}
	if err := json.Unmarshal(m.Payload, &v); err != nil {
		return v, fmt.Errorf("%s: %w", m.Type, err)
	}
	return v, nil
}

// RequestUsage asks the daemon for the engine's own utilisation figure.
type RequestUsage struct {
	RequestID string `json:"request_id"`
}

// UsageReport answers it. Supported=false means the engine cannot ask its
// provider — then the platform's own estimate stays the source, which for at
// least one engine is the only one there is.
type UsageReport struct {
	RequestID string `json:"request_id"`
	Supported bool   `json:"supported"`
	Usage     Usage  `json:"usage"`
	Error     string `json:"error,omitempty"`
}
