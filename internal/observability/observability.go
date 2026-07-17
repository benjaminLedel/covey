// Package observability: Session-Recording (append-only), Cost-Tracking und
// Approval-Queue (spec/06). Recording ist die Grundlage für Audit, Debugging
// und Kostenanalyse — unveränderlich, pro Agent/Aufgabe navigierbar.
package observability

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Event-Kinds im Recording.
const (
	KindRuntime    = "runtime"    // Runtime-Event (LLM-/Tool-Call aus stream-json)
	KindLifecycle  = "lifecycle"  // Zustandsübergänge des Agenten/der Aufgabe
	KindCredential = "credential" // Credential-Request (gewährt/verweigert)
	KindApproval   = "approval"   // Approval-Gate angefragt/entschieden
	KindGuardrail  = "guardrail"  // ausgelöste Guard-Rail (geblockt/gegated)
	KindAction     = "action"     // Aktion im Zielsystem über den Action-Proxy
)

type RecordingEvent struct {
	ID        int64           `json:"id"`
	OrgID     uuid.UUID       `json:"org_id"`
	AgentID   uuid.UUID       `json:"agent_id"`
	TaskID    *uuid.UUID      `json:"task_id,omitempty"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

type Approval struct {
	ID          uuid.UUID       `json:"id"`
	OrgID       uuid.UUID       `json:"org_id"`
	AgentID     uuid.UUID       `json:"agent_id"`
	TaskID      *uuid.UUID      `json:"task_id,omitempty"`
	Action      string          `json:"action"`
	Params      json.RawMessage `json:"params"`
	Status      string          `json:"status"`
	RequestedAt time.Time       `json:"requested_at"`
	DecidedAt   *time.Time      `json:"decided_at,omitempty"`
	DecidedBy   *uuid.UUID      `json:"decided_by,omitempty"`
}

type CostSummary struct {
	AgentID      uuid.UUID `json:"agent_id"`
	TotalUSD     float64   `json:"total_usd"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	Entries      int64     `json:"entries"`
}

var ErrNotFound = errors.New("nicht gefunden")

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Record schreibt ein Ereignis ins Recording. payload wird als JSON persistiert.
func (s *Store) Record(ctx context.Context, orgID, agentID uuid.UUID, taskID *uuid.UUID, kind string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO recording_events (org_id, agent_id, task_id, kind, payload)
		VALUES ($1,$2,$3,$4,$5)`, orgID, agentID, taskID, kind, raw)
	return err
}

// Events liefert die Recording-Timeline, optional pro Aufgabe, seit einer ID (Live-Follow).
func (s *Store) Events(ctx context.Context, agentID uuid.UUID, taskID *uuid.UUID, afterID int64, limit int) ([]RecordingEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, `SELECT id, org_id, agent_id, task_id, kind, payload, created_at
		FROM recording_events
		WHERE agent_id=$1 AND ($2::uuid IS NULL OR task_id=$2) AND id > $3
		ORDER BY id LIMIT $4`, agentID, taskID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecordingEvent
	for rows.Next() {
		var e RecordingEvent
		if err := rows.Scan(&e.ID, &e.OrgID, &e.AgentID, &e.TaskID, &e.Kind, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// OrgEventsByKind liefert die jüngsten Ereignisse einer Art org-weit,
// neueste zuerst — z. B. alle ausgelösten Guard-Rails für das Audit-Feed.
func (s *Store) OrgEventsByKind(ctx context.Context, orgID uuid.UUID, kind string, limit int) ([]RecordingEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT id, org_id, agent_id, task_id, kind, payload, created_at
		FROM recording_events
		WHERE org_id=$1 AND kind=$2
		ORDER BY id DESC LIMIT $3`, orgID, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecordingEvent
	for rows.Next() {
		var e RecordingEvent
		if err := rows.Scan(&e.ID, &e.OrgID, &e.AgentID, &e.TaskID, &e.Kind, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AddCost verbucht Kosten aus einem cost-Event des Daemons.
func (s *Store) AddCost(ctx context.Context, agentID uuid.UUID, taskID *uuid.UUID, usd float64, inputTokens, outputTokens int64, model string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO cost_entries (agent_id, task_id, usd, input_tokens, output_tokens, model)
		VALUES ($1,$2,$3,$4,$5,$6)`, agentID, taskID, usd, inputTokens, outputTokens, model)
	return err
}

func (s *Store) CostByAgent(ctx context.Context, agentID uuid.UUID) (CostSummary, error) {
	c := CostSummary{AgentID: agentID}
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(usd),0), COALESCE(SUM(input_tokens),0),
		COALESCE(SUM(output_tokens),0), COUNT(*) FROM cost_entries WHERE agent_id=$1`, agentID).
		Scan(&c.TotalUSD, &c.InputTokens, &c.OutputTokens, &c.Entries)
	return c, err
}

// --- Approval-Queue ---

func (s *Store) CreateApproval(ctx context.Context, orgID, agentID uuid.UUID, taskID *uuid.UUID, action string, params any) (Approval, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return Approval{}, err
	}
	a := Approval{ID: uuid.New(), OrgID: orgID, AgentID: agentID, TaskID: taskID,
		Action: action, Params: raw, Status: "pending"}
	err = s.pool.QueryRow(ctx, `INSERT INTO approvals (id, org_id, agent_id, task_id, action, params)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING requested_at`,
		a.ID, orgID, agentID, taskID, action, raw).Scan(&a.RequestedAt)
	return a, err
}

// DecideApproval entscheidet ein pending-Gate; idempotent gegen Doppel-Klicks.
func (s *Store) DecideApproval(ctx context.Context, orgID, id uuid.UUID, approve bool, decidedBy *uuid.UUID) (Approval, error) {
	status := "denied"
	if approve {
		status = "approved"
	}
	tag, err := s.pool.Exec(ctx, `UPDATE approvals SET status=$3, decided_at=now(), decided_by=$4
		WHERE org_id=$1 AND id=$2 AND status='pending'`, orgID, id, status, decidedBy)
	if err != nil {
		return Approval{}, err
	}
	if tag.RowsAffected() == 0 {
		return Approval{}, ErrNotFound
	}
	return s.GetApproval(ctx, orgID, id)
}

func (s *Store) GetApproval(ctx context.Context, orgID, id uuid.UUID) (Approval, error) {
	row := s.pool.QueryRow(ctx, `SELECT id, org_id, agent_id, task_id, action, params, status, requested_at, decided_at, decided_by
		FROM approvals WHERE org_id=$1 AND id=$2`, orgID, id)
	return scanApproval(row)
}

func scanApproval(row pgx.Row) (Approval, error) {
	var a Approval
	err := row.Scan(&a.ID, &a.OrgID, &a.AgentID, &a.TaskID, &a.Action, &a.Params, &a.Status, &a.RequestedAt, &a.DecidedAt, &a.DecidedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

func (s *Store) ListApprovals(ctx context.Context, orgID uuid.UUID, status string) ([]Approval, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, org_id, agent_id, task_id, action, params, status, requested_at, decided_at, decided_by
		FROM approvals WHERE org_id=$1 AND ($2='' OR status=$2) ORDER BY requested_at DESC LIMIT 200`, orgID, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Approval
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
