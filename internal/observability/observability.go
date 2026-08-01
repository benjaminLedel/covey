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

// Recording-Level (Aufzeichnungstiefe, spec/06): minimal < standard < full.
// full schließt Screenshots/Artefakte ein; darunter werden sie nicht gespeichert.
const (
	LevelMinimal  = "minimal"
	LevelStandard = "standard"
	LevelFull     = "full"
)

var levelRank = map[string]int{LevelMinimal: 0, LevelStandard: 1, LevelFull: 2}

// ValidLevel prüft einen Recording-Level-Wert.
func ValidLevel(s string) bool { _, ok := levelRank[s]; return ok }

// EffectiveRecordingLevel liefert die effektive Aufzeichnungstiefe eines Agenten:
// das Maximum aus Org-Boden und (optionalem) Agent-Override — nie unter dem
// Boden. Bei Fehler/Unbekanntem fällt es fail-safe auf standard.
func (s *Store) EffectiveRecordingLevel(ctx context.Context, agentID uuid.UUID) (string, error) {
	var orgLevel string
	var agentLevel *string
	err := s.pool.QueryRow(ctx, `SELECT o.recording_level, a.recording_level
		FROM agents a JOIN organizations o ON o.id = a.org_id WHERE a.id = $1`, agentID).
		Scan(&orgLevel, &agentLevel)
	if err != nil {
		return LevelStandard, err
	}
	return effectiveLevel(orgLevel, agentLevel), nil
}

// effectiveLevel ist die Regel hinter EffectiveRecordingLevel, getrennt von der
// Abfrage: Der Org-Level ist der **Boden**, ein Agent darf nur nach oben davon
// abweichen. Ein Agent, der sich selbst leiser stellen könnte, wäre genau die
// Lücke, die Security/Compliance mit dem Org-Boden schließen wollten.
// Unbekannte Werte fallen fail-safe auf standard — im Zweifel wird mehr
// aufgezeichnet, nicht weniger.
func effectiveLevel(orgLevel string, agentLevel *string) string {
	eff := orgLevel
	if agentLevel != nil && levelRank[*agentLevel] > levelRank[eff] {
		eff = *agentLevel
	}
	if _, ok := levelRank[eff]; !ok {
		eff = LevelStandard
	}
	return eff
}

// SetOrgRecordingLevel setzt den Org-Boden (Security/Compliance).
func (s *Store) SetOrgRecordingLevel(ctx context.Context, orgID uuid.UUID, level string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE organizations SET recording_level=$2 WHERE id=$1`, orgID, level)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// OrgRecordingLevel liest den Org-Boden.
func (s *Store) OrgRecordingLevel(ctx context.Context, orgID uuid.UUID) (string, error) {
	var level string
	err := s.pool.QueryRow(ctx, `SELECT recording_level FROM organizations WHERE id=$1`, orgID).Scan(&level)
	if errors.Is(err, pgx.ErrNoRows) {
		return LevelStandard, ErrNotFound
	}
	return level, err
}

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

// CostBucket ist ein Zeitfenster (Stunde/Tag/Woche) der Kostenzeitreihe.
type CostBucket struct {
	Period       time.Time `json:"period"`
	TotalUSD     float64   `json:"total_usd"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	Entries      int64     `json:"entries"`
}

// AgentCost ist die Kostensumme eines Agenten für die Org-Aufschlüsselung.
type AgentCost struct {
	AgentID      uuid.UUID `json:"agent_id"`
	Slug         string    `json:"slug"`
	DisplayName  string    `json:"display_name"`
	TotalUSD     float64   `json:"total_usd"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	Entries      int64     `json:"entries"`
}

// ModelCost ist die Kostensumme pro LLM-Modell.
type ModelCost struct {
	Model        string  `json:"model"`
	TotalUSD     float64 `json:"total_usd"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	Entries      int64   `json:"entries"`
}

// OrgCostReport bündelt org-weite Kosten: Gesamtsummen, Zeitreihe,
// Aufschlüsselung pro Agent und pro Modell — die Datenbasis fürs Diagramm.
type OrgCostReport struct {
	TotalUSD     float64      `json:"total_usd"`
	InputTokens  int64        `json:"input_tokens"`
	OutputTokens int64        `json:"output_tokens"`
	Entries      int64        `json:"entries"`
	Bucket       string       `json:"bucket"`
	Series       []CostBucket `json:"series"`
	Agents       []AgentCost  `json:"agents"`
	Models       []ModelCost  `json:"models"`
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

// PutBlob legt ein Binär-Artefakt (z. B. Screenshot) out-of-band ab und liefert
// dessen id — die kommt referenziert in den Event-Payload, nicht die Bytes.
func (s *Store) PutBlob(ctx context.Context, orgID, agentID uuid.UUID, taskID *uuid.UUID, mime string, data []byte) (uuid.UUID, error) {
	id := uuid.New()
	_, err := s.pool.Exec(ctx, `INSERT INTO recording_blobs (id, org_id, agent_id, task_id, mime, bytes)
		VALUES ($1,$2,$3,$4,$5,$6)`, id, orgID, agentID, taskID, mime, data)
	return id, err
}

// GetBlob liefert ein Artefakt org-gescopt (mime + Bytes).
func (s *Store) GetBlob(ctx context.Context, orgID, id uuid.UUID) (string, []byte, error) {
	var mime string
	var data []byte
	err := s.pool.QueryRow(ctx, `SELECT mime, bytes FROM recording_blobs WHERE org_id=$1 AND id=$2`,
		orgID, id).Scan(&mime, &data)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, ErrNotFound
	}
	return mime, data, err
}

// Events liefert die Recording-Timeline, optional pro Aufgabe, seit einer ID (Live-Follow).
func (s *Store) Events(ctx context.Context, agentID uuid.UUID, taskID *uuid.UUID, afterID int64, limit int) ([]RecordingEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	// Ohne after-Cursor interessiert das jüngste Geschehen: die letzten N
	// Events, chronologisch sortiert. Mit Cursor (Live-Follow) weiterhin
	// vorwärts ab der bekannten ID.
	query := `SELECT id, org_id, agent_id, task_id, kind, payload, created_at
		FROM recording_events
		WHERE agent_id=$1 AND ($2::uuid IS NULL OR task_id=$2) AND id > $3
		ORDER BY id LIMIT $4`
	if afterID <= 0 {
		query = `SELECT id, org_id, agent_id, task_id, kind, payload, created_at FROM (
			SELECT id, org_id, agent_id, task_id, kind, payload, created_at
			FROM recording_events
			WHERE agent_id=$1 AND ($2::uuid IS NULL OR task_id=$2) AND id > $3
			ORDER BY id DESC LIMIT $4
		) sub ORDER BY id`
	}
	rows, err := s.pool.Query(ctx, query, agentID, taskID, afterID, limit)
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

// normalizeBucket beschränkt die Zeit-Granularität auf gültige date_trunc-Werte
// (kein SQL-Injection-Vektor, sinnvoller Default).
func normalizeBucket(bucket string) string {
	switch bucket {
	case "hour", "day", "week", "month":
		return bucket
	default:
		return "day"
	}
}

func scanBuckets(rows pgx.Rows) ([]CostBucket, error) {
	defer rows.Close()
	out := []CostBucket{}
	for rows.Next() {
		var b CostBucket
		if err := rows.Scan(&b.Period, &b.TotalUSD, &b.InputTokens, &b.OutputTokens, &b.Entries); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// CostSeriesByAgent liefert die Kostenzeitreihe eines Agenten, in Zeitfenster
// (bucket) gruppiert und ab since. Für das Kosten-/Token-Diagramm.
func (s *Store) CostSeriesByAgent(ctx context.Context, agentID uuid.UUID, bucket string, since time.Time) ([]CostBucket, error) {
	rows, err := s.pool.Query(ctx, `SELECT date_trunc($2, created_at) AS period,
		COALESCE(SUM(usd),0), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COUNT(*)
		FROM cost_entries WHERE agent_id=$1 AND created_at >= $3
		GROUP BY period ORDER BY period`, agentID, normalizeBucket(bucket), since)
	if err != nil {
		return nil, err
	}
	return scanBuckets(rows)
}

// OrgCostReport aggregiert die Kosten einer Organisation: Gesamtsummen,
// Zeitreihe, Aufschlüsselung pro Agent und pro Modell. cost_entries hat keine
// org_id — wir joinen deshalb über agents.
func (s *Store) OrgCostReport(ctx context.Context, orgID uuid.UUID, bucket string, since time.Time) (OrgCostReport, error) {
	rep := OrgCostReport{Bucket: normalizeBucket(bucket), Series: []CostBucket{}, Agents: []AgentCost{}, Models: []ModelCost{}}

	// Gesamtsummen.
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(ce.usd),0), COALESCE(SUM(ce.input_tokens),0),
		COALESCE(SUM(ce.output_tokens),0), COUNT(*)
		FROM cost_entries ce JOIN agents a ON a.id=ce.agent_id
		WHERE a.org_id=$1 AND ce.created_at >= $2`, orgID, since).
		Scan(&rep.TotalUSD, &rep.InputTokens, &rep.OutputTokens, &rep.Entries); err != nil {
		return rep, err
	}

	// Zeitreihe.
	rows, err := s.pool.Query(ctx, `SELECT date_trunc($2, ce.created_at) AS period,
		COALESCE(SUM(ce.usd),0), COALESCE(SUM(ce.input_tokens),0), COALESCE(SUM(ce.output_tokens),0), COUNT(*)
		FROM cost_entries ce JOIN agents a ON a.id=ce.agent_id
		WHERE a.org_id=$1 AND ce.created_at >= $3
		GROUP BY period ORDER BY period`, orgID, rep.Bucket, since)
	if err != nil {
		return rep, err
	}
	if rep.Series, err = scanBuckets(rows); err != nil {
		return rep, err
	}

	// Pro Agent.
	arows, err := s.pool.Query(ctx, `SELECT a.id, a.slug, a.display_name,
		COALESCE(SUM(ce.usd),0), COALESCE(SUM(ce.input_tokens),0), COALESCE(SUM(ce.output_tokens),0), COUNT(ce.id)
		FROM agents a LEFT JOIN cost_entries ce ON ce.agent_id=a.id AND ce.created_at >= $2
		WHERE a.org_id=$1
		GROUP BY a.id, a.slug, a.display_name
		HAVING COUNT(ce.id) > 0
		ORDER BY SUM(ce.usd) DESC NULLS LAST`, orgID, since)
	if err != nil {
		return rep, err
	}
	func() {
		defer arows.Close()
		for arows.Next() {
			var ac AgentCost
			if err = arows.Scan(&ac.AgentID, &ac.Slug, &ac.DisplayName, &ac.TotalUSD, &ac.InputTokens, &ac.OutputTokens, &ac.Entries); err != nil {
				return
			}
			rep.Agents = append(rep.Agents, ac)
		}
		err = arows.Err()
	}()
	if err != nil {
		return rep, err
	}

	// Pro Modell.
	mrows, err := s.pool.Query(ctx, `SELECT COALESCE(NULLIF(ce.model,''),'unbekannt'),
		COALESCE(SUM(ce.usd),0), COALESCE(SUM(ce.input_tokens),0), COALESCE(SUM(ce.output_tokens),0), COUNT(*)
		FROM cost_entries ce JOIN agents a ON a.id=ce.agent_id
		WHERE a.org_id=$1 AND ce.created_at >= $2
		GROUP BY 1 ORDER BY SUM(ce.usd) DESC`, orgID, since)
	if err != nil {
		return rep, err
	}
	defer mrows.Close()
	for mrows.Next() {
		var mc ModelCost
		if err := mrows.Scan(&mc.Model, &mc.TotalUSD, &mc.InputTokens, &mc.OutputTokens, &mc.Entries); err != nil {
			return rep, err
		}
		rep.Models = append(rep.Models, mc)
	}
	return rep, mrows.Err()
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
