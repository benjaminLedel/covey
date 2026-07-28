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
	TypeInjectTarget      = "inject_target"
	TypeInjectOrgChart    = "inject_org_chart"
	TypeInjectWiki        = "inject_wiki"
	TypeKill              = "kill"
	TypeSleep             = "sleep"
)

// Daemon → Control Plane.
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
)

type InjectConfig struct {
	SystemPrompt string   `json:"system_prompt"`
	Runtime      string   `json:"runtime"`
	Model        string   `json:"model,omitempty"` // leer = Runtime-Default
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

// RequestTarget/InjectTarget brokern die Definition eines Manifest-Plugins
// (kind=custom) in die Sandbox: Der Daemon kennt kompilierte Plugins selbst,
// hochgeladene Manifeste holt er sich von der Control Plane — nur wenn das
// System für die Organisation aktiviert ist (fail-closed).
type RequestTarget struct {
	RequestID string `json:"request_id"`
	System    string `json:"system"`
}

type InjectTarget struct {
	RequestID string `json:"request_id"`
	System    string `json:"system"`
	Granted   bool   `json:"granted"`
	Reason    string `json:"reason,omitempty"`
	// Kind unterscheidet die Definition in Manifest: "custom" (REST-Manifest)
	// oder "mcp" (MCP-Server-Config). Leer = custom (Rückwärtskompatibilität).
	Kind     string          `json:"kind,omitempty"`
	Manifest json.RawMessage `json:"manifest,omitempty"`
}

// RequestOrgChart/InjectOrgChart brokern das Organigramm der Organisation in
// die Sandbox: Menschen und Agenten samt Profilen (inkl. der konfigurierbaren
// Profilfelder), Abteilungen und Vorgesetzten-Beziehungen. Read-only-Sicht auf
// dieselben Daten wie GET /org/chart — der Agent fragt sie zur Laufzeit über
// die Meta-Aktion covey/org_chart ab, statt einen veralteten Prompt-Stand zu
// nutzen.
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

// TaskDone schließt eine Aufgabe ab. Status "incomplete" ist der Sonderfall des
// am Turn-Limit abgebrochenen Laufs: Er hat gearbeitet, aber kein Ergebnis
// erreicht. Result trägt dann den Übergabe-Stand („Erledigt / Offen / Nächster
// Schritt"), den sich der Adapter aus der abgebrochenen Session geben lässt, und
// SessionID die Session zum Wiederaufsetzen. Die Control Plane macht daraus eine
// Folgeaufgabe statt eines stillen Fehlschlags (siehe orchestrator.handleIncomplete).
type TaskDone struct {
	TaskID    string `json:"task_id"`
	Status    string `json:"status"` // done | failed | escalated | incomplete
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

// Note ist eine proaktive Notiz des Agenten mitten im Lauf. Scope "task" hängt
// sie an die Aufgabe (Zwischenstände, Befunde — aufgabenbezogen), Scope
// "memory" speist sie sofort ins Gedächtnis (allgemeingültige Erkenntnisse) —
// ohne auf das memory-Feld von task_done zu warten. TaskID leer = aktuelle Aufgabe.
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
	Model        string  `json:"model,omitempty"`
}

// RequestWiki/InjectWiki brokern die Wiki-Tools des Agenten (covey/wiki_*) und
// die Home-Arbeitskopie in die Control Plane: search (Vektorsuche über die
// Seiten), read (eine Seite per Slug), write (Seite anlegen/aktualisieren,
// [[slug]]-Wikilinks im Body) und list (alle Seiten fürs Materialisieren ins
// Home). Das Wiki liegt in der Control Plane (Quelle der Wahrheit, spec/05).
type RequestWiki struct {
	RequestID string `json:"request_id"`
	Op        string `json:"op"` // search | read | write | list
	Query     string `json:"query,omitempty"`
	Slug      string `json:"slug,omitempty"`
	Title     string `json:"title,omitempty"`
	Body      string `json:"body,omitempty"`
}

type InjectWiki struct {
	RequestID string          `json:"request_id"`
	OK        bool            `json:"ok"`
	Error     string          `json:"error,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
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
