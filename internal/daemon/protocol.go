// Package daemon enthält das bidirektionale Daemon-Protokoll (spec/01) und
// die Daemon-Seite (Runtime-Adapter, Action-Proxy). Das Protokoll ist die
// stabile Naht des Systems: Runtimes ändern sich, das Protokoll bleibt.
package daemon

import (
	"encoding/json"
	"fmt"
)

// Message ist der Envelope beider Richtungen (JSON über WebSocket).
type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Control Plane → Daemon. ("wake" ist kein WS-Frame: Wecken = die Control
// Plane startet die Sandbox über den SandboxProvider; danach verbindet sich
// der Daemon und meldet ready.)
const (
	TypeInjectConfig      = "inject_config"
	TypeAssignTask        = "assign_task"
	TypeInjectCredentials = "inject_credentials"
	TypeApprovalDecision  = "approval_decision"
	TypeKill              = "kill"
	TypeSleep             = "sleep"
)

// Daemon → Control Plane.
const (
	TypeReady             = "ready"
	TypeEvent             = "event"
	TypeRequestCredential = "request_credential"
	TypeRequestApproval   = "request_approval"
	TypeBlocked           = "blocked"
	TypeTaskDone          = "task_done"
	TypeCost              = "cost"
	TypeHeartbeat         = "heartbeat"
	TypeSetStage          = "set_stage"
)

type InjectConfig struct {
	SystemPrompt string   `json:"system_prompt"`
	Runtime      string   `json:"runtime"`
	AllowedTools []string `json:"allowed_tools,omitempty"`
	MaxTurns     int      `json:"max_turns,omitempty"`
	MaxBudgetUSD float64  `json:"max_budget_usd,omitempty"`
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
	TTLSecs   int    `json:"ttl_secs,omitempty"`
	// EnvVar nennt die Ziel-Umgebungsvariable der Runtime, wenn die Control
	// Plane den Credential-Typ kennt (aus dem Secret-Namen). Ist sie leer,
	// rät der Daemon anhand des Token-Präfixes (Rückwärtskompatibilität).
	EnvVar string `json:"env_var,omitempty"`
}

// ApprovalDecision ist die zentrale Policy-Entscheidung zu einer Aktion:
// approved (ggf. auto-allow), denied (Guard-Rail) oder pending (wartet auf
// menschliche Freigabe; correlation_key weckt die Aufgabe nach der Entscheidung).
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
	Kind    string          `json:"kind"` // runtime | action | tool_use ...
	Payload json.RawMessage `json:"payload"`
}

type RequestCredential struct {
	RequestID string   `json:"request_id"`
	System    string   `json:"system"`
	Scopes    []string `json:"scopes,omitempty"`
	TaskID    string   `json:"task_id,omitempty"`
}

type RequestApproval struct {
	RequestID string          `json:"request_id"`
	TaskID    string          `json:"task_id,omitempty"`
	Action    string          `json:"action"` // z. B. "zammad:reply_external"
	Params    json.RawMessage `json:"params"`
}

type Blocked struct {
	TaskID         string `json:"task_id"`
	CorrelationKey string `json:"correlation_key"`
	Question       string `json:"question,omitempty"`
	SessionID      string `json:"session_id,omitempty"` // Runtime-Session für --resume
}

type TaskDone struct {
	TaskID    string `json:"task_id"`
	Status    string `json:"status"` // done | failed | escalated
	Result    string `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
	Memory    string `json:"memory,omitempty"` // Episode fürs Gedächtnis (M7)
	SessionID string `json:"session_id,omitempty"`
}

// SetStage bewegt eine Aufgabe auf dem Kanban-Board in eine (ggf. neue) Stage.
// Rein anzeigend — kein Lifecycle-Übergang. Ist Stage neu, legt die Control
// Plane sie an ("der Agent erfindet Spalten"). TaskID leer = aktuelle Aufgabe.
type SetStage struct {
	TaskID string `json:"task_id,omitempty"`
	Stage  string `json:"stage"`
}

type Cost struct {
	TaskID       string  `json:"task_id,omitempty"`
	USD          float64 `json:"usd"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	Model        string  `json:"model,omitempty"`
}

// Encode baut einen Envelope; panict nie, weil alle Payloads marshalbar sind.
func Encode(msgType string, payload any) (Message, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Message{}, fmt.Errorf("encode %s: %w", msgType, err)
	}
	return Message{Type: msgType, Payload: raw}, nil
}

// DecodePayload entpackt den Payload eines Envelopes typisiert.
func DecodePayload[T any](m Message) (T, error) {
	var v T
	if len(m.Payload) == 0 {
		return v, fmt.Errorf("%s: leerer payload", m.Type)
	}
	if err := json.Unmarshal(m.Payload, &v); err != nil {
		return v, fmt.Errorf("%s: %w", m.Type, err)
	}
	return v, nil
}
