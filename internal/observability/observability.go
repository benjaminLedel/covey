// Package observability: session recording (append-only), cost tracking and
// approval queue (spec/06). The recording is the basis for audit, debugging and
// cost analysis — immutable, navigable per agent/task.
package observability

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Event kinds in the recording.
const (
	KindRuntime    = "runtime"    // runtime event (LLM/tool call from stream-json)
	KindLifecycle  = "lifecycle"  // state transitions of the agent/the task
	KindCredential = "credential" // credential request (granted/denied)
	KindApproval   = "approval"   // approval gate requested/decided
	KindGuardrail  = "guardrail"  // triggered guard-rail (blocked/gated)
	KindAction     = "action"     // action in the target system via the action proxy
)

// Recording levels (recording depth, spec/06): minimal < standard < full.
// full includes screenshots/artifacts; below that they are not stored.
const (
	LevelMinimal  = "minimal"
	LevelStandard = "standard"
	LevelFull     = "full"
)

var levelRank = map[string]int{LevelMinimal: 0, LevelStandard: 1, LevelFull: 2}

// ValidLevel checks a recording-level value.
func ValidLevel(s string) bool { _, ok := levelRank[s]; return ok }

// EffectiveRecordingLevel returns an agent's effective recording depth: the
// maximum of the org floor and the (optional) agent override — never below the
// floor. On error/unknown values it falls back fail-safe to standard.
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

// effectiveLevel is the rule behind EffectiveRecordingLevel, kept apart from
// the query: the org level is the **floor**, an agent may only deviate upwards
// from it. An agent that could turn itself down would be exactly the gap that
// security/compliance wanted to close with the org floor. Unknown values fall
// back fail-safe to standard — in doubt more is recorded, not less.
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

// SetOrgRecordingLevel sets the org floor (security/compliance).
func (s *Store) SetOrgRecordingLevel(ctx context.Context, orgID uuid.UUID, level string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE organizations SET recording_level=$2 WHERE id=$1`, orgID, level)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// OrgRecordingLevel reads the org floor.
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

// Tokens is the token side of a cost total.
//
// Three fields rather than one, because the three are priced differently: fresh
// input, a read out of the prompt cache (about a tenth of it) and writing the
// cache (about a quarter more). Input alone used to be reported — with Claude
// Code that is the smallest of the three by far and made every token figure in
// the interface look absurd (5,497 in against 1,842,222 out for one agent).
type Tokens struct {
	// Input is the UNCACHED input only — keep TotalInput() in mind when
	// displaying it.
	Input         int64 `json:"input_tokens"`
	Output        int64 `json:"output_tokens"`
	CacheRead     int64 `json:"cache_read_tokens"`
	CacheCreation int64 `json:"cache_creation_tokens"`
}

// TotalInput is the input side as a human means it: everything read, cached or
// not.
func (t Tokens) TotalInput() int64 { return t.Input + t.CacheRead + t.CacheCreation }

type CostSummary struct {
	AgentID  uuid.UUID `json:"agent_id"`
	TotalUSD float64   `json:"total_usd"`
	Tokens
	Entries int64 `json:"entries"`
}

// CostBucket is one time window (hour/day/week) of the cost time series.
type CostBucket struct {
	Period   time.Time `json:"period"`
	TotalUSD float64   `json:"total_usd"`
	Tokens
	Entries int64 `json:"entries"`
}

// AgentCost is an agent's cost total for the org breakdown.
type AgentCost struct {
	AgentID     uuid.UUID `json:"agent_id"`
	Slug        string    `json:"slug"`
	DisplayName string    `json:"display_name"`
	TotalUSD    float64   `json:"total_usd"`
	Tokens
	Entries int64 `json:"entries"`
}

// ModelCost is the cost total per LLM model.
type ModelCost struct {
	Model    string  `json:"model"`
	TotalUSD float64 `json:"total_usd"`
	Tokens
	Entries int64 `json:"entries"`
}

// RunCost is what ONE run cost — a single backlog task with its consumption.
//
// It exists because the aggregates above answer "how much did the day cost" but
// not "which run cost it". The costs were bookable per task from the start
// (AddCost takes a task_id), the number just never came back out; whoever wanted
// to know where a burning day came from had to correlate cost buckets with the
// backlog by hand over timestamps.
//
// Actions is the decisive column next to the money: the number of recorded
// target-system actions of the run. A run with actions=0 changed nothing
// outside itself — it read, thought and went back to sleep. That is the
// difference between an agent that works and one that only wakes up, and
// without it an expensive idle run looks in every statistic exactly like an
// expensive productive one.
type RunCost struct {
	TaskID   uuid.UUID `json:"task_id"`
	AgentID  uuid.UUID `json:"agent_id"`
	Slug     string    `json:"slug"`
	Title    string    `json:"title"`
	State    string    `json:"state"`
	Origin   string    `json:"origin"`
	TotalUSD float64   `json:"total_usd"`
	Tokens
	Entries   int64     `json:"entries"`
	Actions   int64     `json:"actions"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
}

// Indicator is a counting rule as the evaluation needs it (spec/17-kpis.md).
//
// Deliberately its own type and not the parsed `KPIS.md` entry: this package
// counts, it does not know the config language. The translation happens where
// both sides are known — a rule that arrives here has already been validated.
type Indicator struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	// Action is the counted target-system action ("zammad:reply_external"), or
	// "<system>:*" for any action of a system. Empty means the task form.
	Action string `json:"action,omitempty"`
	// Origin narrows the task form to a task origin.
	Origin string `json:"origin,omitempty"`
	// Per is the parameter identifying the business object. With it the count
	// becomes DISTINCT: five replies in the same ticket are one resolved
	// ticket, not five.
	Per    string `json:"per,omitempty"`
	Goal   int    `json:"goal,omitempty"`
	Period string `json:"period,omitempty"`
}

// IndicatorResult is one line of the price list: how often, and at what price.
//
// UnitUSD is a pointer because below minCountForUnitCost there is no price —
// a unit cost computed over three events is noise, and shown as a number it
// ranks next to one computed over three hundred as if they were the same kind
// of statement.
type IndicatorResult struct {
	Indicator
	Count   int64    `json:"count"`
	UnitUSD *float64 `json:"unit_usd,omitempty"`
}

// minCountForUnitCost is the number of events from which a unit cost is a
// measurement rather than an accident.
const minCountForUnitCost = 5

// TaskCost is what ONE task cost — the small form of RunCost, without the
// backlog columns a caller that holds the task already has in hand.
type TaskCost struct {
	TotalUSD float64 `json:"total_usd"`
	Entries  int64   `json:"entries"`
}

// OrgCostReport bundles org-wide costs: totals, time series, breakdown per
// agent and per model — the data basis for the chart.
type OrgCostReport struct {
	TotalUSD float64 `json:"total_usd"`
	Tokens
	Entries int64        `json:"entries"`
	Bucket  string       `json:"bucket"`
	Series  []CostBucket `json:"series"`
	Agents  []AgentCost  `json:"agents"`
	Models  []ModelCost  `json:"models"`
}

// tokenSums is the token part of every cost aggregation — one place, so a
// fourth token sort does not have to be chased through five queries. The prefix
// is the table alias ("" or "ce.").
func tokenSums(prefix string) string {
	cols := []string{"input_tokens", "output_tokens", "cache_read_tokens", "cache_creation_tokens"}
	for i, c := range cols {
		cols[i] = "COALESCE(SUM(" + prefix + c + "),0)"
	}
	return strings.Join(cols, ", ")
}

var ErrNotFound = errors.New("not found")

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Record writes an event into the recording. payload is persisted as JSON.
func (s *Store) Record(ctx context.Context, orgID, agentID uuid.UUID, taskID *uuid.UUID, kind string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO recording_events (org_id, agent_id, task_id, kind, payload)
		VALUES ($1,$2,$3,$4,$5)`, orgID, agentID, taskID, kind, raw)
	return err
}

// PutBlob stores a binary artifact (e.g. a screenshot) out-of-band and returns
// its id — that goes into the event payload as a reference, not the bytes.
func (s *Store) PutBlob(ctx context.Context, orgID, agentID uuid.UUID, taskID *uuid.UUID, mime string, data []byte) (uuid.UUID, error) {
	id := uuid.New()
	_, err := s.pool.Exec(ctx, `INSERT INTO recording_blobs (id, org_id, agent_id, task_id, mime, bytes)
		VALUES ($1,$2,$3,$4,$5,$6)`, id, orgID, agentID, taskID, mime, data)
	return id, err
}

// GetBlob returns an artifact org-scoped (mime + bytes).
// Blob is a recording artifact (screenshot) including its content.
type Blob struct {
	ID        uuid.UUID `json:"id"`
	MIME      string    `json:"mime"`
	Bytes     []byte    `json:"bytes"`
	CreatedAt time.Time `json:"created_at"`
}

// BlobsByAgent returns all artifacts of an agent — with content, because the
// only caller is the diagnostic export, which produces a complete image. For
// views there is GetBlob (a single one, org-checked).
func (s *Store) BlobsByAgent(ctx context.Context, agentID uuid.UUID) ([]Blob, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT id, mime, bytes, created_at FROM recording_blobs WHERE agent_id=$1 ORDER BY created_at", agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Blob
	for rows.Next() {
		var b Blob
		if err := rows.Scan(&b.ID, &b.MIME, &b.Bytes, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

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

// Events returns the recording timeline, optionally per task, since an ID
// (live follow).
func (s *Store) Events(ctx context.Context, agentID uuid.UUID, taskID *uuid.UUID, afterID int64, limit int) ([]RecordingEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	// Without an after cursor the most recent happenings are what matters: the
	// last N events, sorted chronologically. With a cursor (live follow) still
	// forwards from the known ID.
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

// OrgEventsByKind returns the most recent events of one kind org-wide, newest
// first — e.g. all triggered guard-rails for the audit feed.
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

// AddCost books costs from a cost event of the daemon.
func (s *Store) AddCost(ctx context.Context, agentID uuid.UUID, taskID *uuid.UUID, usd float64, tok Tokens, model string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO cost_entries
		(agent_id, task_id, usd, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, model)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		agentID, taskID, usd, tok.Input, tok.Output, tok.CacheRead, tok.CacheCreation, model)
	return err
}

// CostByTasks liefert die Kosten einer Menge von Aufgaben in einem Rutsch —
// task_id → Summe. Gedacht für Listenansichten (das Backlog), die neben jeder
// Aufgabe zeigen wollen, was sie gekostet hat: einzeln abgefragt wäre das eine
// Query pro Karte.
//
// Aufgaben ohne Kosten fehlen in der Map. Das ist der ehrliche Zustand und
// nicht dasselbe wie 0,00 $: eine Aufgabe, die noch wartet, hat noch nichts
// gekostet — eine, die lief und nichts kostete, gibt es praktisch nicht.
func (s *Store) CostByTasks(ctx context.Context, taskIDs []uuid.UUID) (map[uuid.UUID]TaskCost, error) {
	out := map[uuid.UUID]TaskCost{}
	if len(taskIDs) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT task_id, COALESCE(SUM(usd),0), COUNT(*)
		FROM cost_entries WHERE task_id = ANY($1) GROUP BY task_id`, taskIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var c TaskCost
		if err := rows.Scan(&id, &c.TotalUSD, &c.Entries); err != nil {
			return nil, err
		}
		out[id] = c
	}
	return out, rows.Err()
}

// CountIndicator counts one indicator over the given agents since a point in
// time. The agent set is the caller's business: org-wide an indicator is
// counted over exactly those agents that carry its key — otherwise the price of
// a resolved ticket would include a QA agent that never touched one.
//
// Only successful actions count. An agent that tried to close a ticket and got
// a 422 resolved nothing, and a figure that counts the attempt measures effort,
// not delivery.
func (s *Store) CountIndicator(ctx context.Context, ind Indicator, agentIDs []uuid.UUID, since time.Time) (int64, error) {
	if len(agentIDs) == 0 {
		return 0, nil
	}
	var n int64
	if ind.Action == "" {
		// The task form: a backlog task that reached `done`. updated_at is the
		// moment of the last transition, which for a terminal task is when it
		// got there.
		err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM backlog_tasks
			WHERE agent_id = ANY($1) AND state = 'done' AND updated_at >= $2
			  AND ($3 = '' OR origin = $3)`, agentIDs, since, ind.Origin).Scan(&n)
		return n, err
	}

	// The counted expression: with `je:` the business object, otherwise the
	// events themselves.
	what := "COUNT(*)"
	if ind.Per != "" {
		what = "COUNT(DISTINCT payload->'params'->>'" + sanitizeParam(ind.Per) + "')"
	}
	// A wildcard cannot use the index as a range scan (the collation decides
	// how prefixes compare), so it scans the partial index. Acceptable: the
	// form exists for the coarse "did it touch GitLab at all" and is rare.
	match, arg := "payload->>'action' = $3", ind.Action
	if strings.HasSuffix(ind.Action, ":*") {
		match, arg = "payload->>'action' LIKE $3", strings.TrimSuffix(ind.Action, "*")+"%"
	}
	err := s.pool.QueryRow(ctx, `SELECT `+what+` FROM recording_events
		WHERE agent_id = ANY($1) AND kind = 'action' AND created_at >= $2
		  AND `+match+` AND payload->>'ok' = 'true'`, agentIDs, since, arg).Scan(&n)
	return n, err
}

// sanitizeParam keeps the `je:` parameter name out of the SQL string. It cannot
// be a bind parameter (it names a JSON key inside the expression), so it is
// restricted instead — the parser only admits this shape, and this is the
// second lock on the same door.
func sanitizeParam(p string) string {
	var b strings.Builder
	for _, r := range p {
		if r == '_' || r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// CostOfAgents is the denominator of the unit cost: everything these agents
// cost in the period — including the runs that delivered nothing. Those are
// paid for too, and a unit cost that quietly drops them flatters the agent.
func (s *Store) CostOfAgents(ctx context.Context, agentIDs []uuid.UUID, since time.Time) (float64, error) {
	if len(agentIDs) == 0 {
		return 0, nil
	}
	var usd float64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(usd),0) FROM cost_entries
		WHERE agent_id = ANY($1) AND created_at >= $2`, agentIDs, since).Scan(&usd)
	return usd, err
}

// FailedTasks counts the runs that ended without a result — the counter-figure
// that keeps the price list honest. Without it an agent that abandons every
// hard case looks excellent on what remains.
func (s *Store) FailedTasks(ctx context.Context, agentIDs []uuid.UUID, since time.Time) (int64, error) {
	if len(agentIDs) == 0 {
		return 0, nil
	}
	var n int64
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM backlog_tasks
		WHERE agent_id = ANY($1) AND state IN ('failed','cancelled') AND updated_at >= $2`,
		agentIDs, since).Scan(&n)
	return n, err
}

// UnitCost prices one indicator line, or leaves it unpriced below the minimum
// count. Separate from the query so the rule sits in one place and is testable
// without a database.
func UnitCost(totalUSD float64, count int64) *float64 {
	if count < minCountForUnitCost || totalUSD <= 0 {
		return nil
	}
	u := totalUSD / float64(count)
	return &u
}

func (s *Store) CostByAgent(ctx context.Context, agentID uuid.UUID) (CostSummary, error) {
	c := CostSummary{AgentID: agentID}
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(usd),0), `+tokenSums("")+`, COUNT(*)
		FROM cost_entries WHERE agent_id=$1`, agentID).
		Scan(&c.TotalUSD, &c.Input, &c.Output, &c.CacheRead, &c.CacheCreation, &c.Entries)
	return c, err
}

// normalizeBucket restricts the time granularity to valid date_trunc values
// (no SQL injection vector, sensible default).
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
		if err := rows.Scan(&b.Period, &b.TotalUSD, &b.Input, &b.Output, &b.CacheRead, &b.CacheCreation, &b.Entries); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// CostSeriesByAgent returns an agent's cost time series, grouped into time
// windows (bucket) and starting at since. For the cost/token chart.
func (s *Store) CostSeriesByAgent(ctx context.Context, agentID uuid.UUID, bucket string, since time.Time) ([]CostBucket, error) {
	rows, err := s.pool.Query(ctx, `SELECT date_trunc($2, created_at) AS period,
		COALESCE(SUM(usd),0), `+tokenSums("")+`, COUNT(*)
		FROM cost_entries WHERE agent_id=$1 AND created_at >= $3
		GROUP BY period ORDER BY period`, agentID, normalizeBucket(bucket), since)
	if err != nil {
		return nil, err
	}
	return scanBuckets(rows)
}

// RunCosts returns the organization's most expensive runs since `since`,
// costliest first — with agentID set, only that agent's.
//
// Grouped by task, not by cost entry: a run books several entries (the run
// itself, its sub-runs), and what a human wants to know is what the RUN cost.
// Runs whose task has since been deleted fall out of the join; their cost stays
// in the aggregates, so the sum of the runs may be smaller than the day's total
// — that is intended, this list ranks, it does not settle accounts.
//
// The action count comes from the recording as a separate aggregation instead
// of a second join: joined in directly it would multiply the cost sums by the
// number of events.
func (s *Store) RunCosts(ctx context.Context, orgID uuid.UUID, agentID *uuid.UUID, since time.Time, limit int) ([]RunCost, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.agent_id, a.slug, t.title, t.state, t.origin,
		       COALESCE(SUM(ce.usd),0), `+tokenSums("ce.")+`, COUNT(ce.id),
		       COALESCE((SELECT COUNT(*) FROM recording_events re
		                 WHERE re.task_id = t.id AND re.kind = 'action'), 0),
		       t.created_at, t.updated_at
		FROM cost_entries ce
		JOIN backlog_tasks t ON t.id = ce.task_id
		JOIN agents a ON a.id = t.agent_id
		WHERE a.org_id = $1 AND ce.created_at >= $2 AND ($3::uuid IS NULL OR t.agent_id = $3)
		GROUP BY t.id, t.agent_id, a.slug, t.title, t.state, t.origin, t.created_at, t.updated_at
		ORDER BY SUM(ce.usd) DESC
		LIMIT $4`, orgID, since, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunCost{}
	for rows.Next() {
		var r RunCost
		if err := rows.Scan(&r.TaskID, &r.AgentID, &r.Slug, &r.Title, &r.State, &r.Origin,
			&r.TotalUSD, &r.Input, &r.Output, &r.CacheRead, &r.CacheCreation,
			&r.Entries, &r.Actions, &r.StartedAt, &r.EndedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// OrgCostReport aggregates an organization's costs: totals, time series,
// breakdown per agent and per model. cost_entries has no org_id — we therefore
// join via agents.
func (s *Store) OrgCostReport(ctx context.Context, orgID uuid.UUID, bucket string, since time.Time) (OrgCostReport, error) {
	rep := OrgCostReport{Bucket: normalizeBucket(bucket), Series: []CostBucket{}, Agents: []AgentCost{}, Models: []ModelCost{}}

	// Totals.
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(ce.usd),0), `+tokenSums("ce.")+`, COUNT(*)
		FROM cost_entries ce JOIN agents a ON a.id=ce.agent_id
		WHERE a.org_id=$1 AND ce.created_at >= $2`, orgID, since).
		Scan(&rep.TotalUSD, &rep.Input, &rep.Output, &rep.CacheRead, &rep.CacheCreation, &rep.Entries); err != nil {
		return rep, err
	}

	// Time series.
	rows, err := s.pool.Query(ctx, `SELECT date_trunc($2, ce.created_at) AS period,
		COALESCE(SUM(ce.usd),0), `+tokenSums("ce.")+`, COUNT(*)
		FROM cost_entries ce JOIN agents a ON a.id=ce.agent_id
		WHERE a.org_id=$1 AND ce.created_at >= $3
		GROUP BY period ORDER BY period`, orgID, rep.Bucket, since)
	if err != nil {
		return rep, err
	}
	if rep.Series, err = scanBuckets(rows); err != nil {
		return rep, err
	}

	// Per agent.
	arows, err := s.pool.Query(ctx, `SELECT a.id, a.slug, a.display_name,
		COALESCE(SUM(ce.usd),0), `+tokenSums("ce.")+`, COUNT(ce.id)
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
			if err = arows.Scan(&ac.AgentID, &ac.Slug, &ac.DisplayName, &ac.TotalUSD,
				&ac.Input, &ac.Output, &ac.CacheRead, &ac.CacheCreation, &ac.Entries); err != nil {
				return
			}
			rep.Agents = append(rep.Agents, ac)
		}
		err = arows.Err()
	}()
	if err != nil {
		return rep, err
	}

	// Per model.
	mrows, err := s.pool.Query(ctx, `SELECT COALESCE(NULLIF(ce.model,''),'unknown'),
		COALESCE(SUM(ce.usd),0), `+tokenSums("ce.")+`, COUNT(*)
		FROM cost_entries ce JOIN agents a ON a.id=ce.agent_id
		WHERE a.org_id=$1 AND ce.created_at >= $2
		GROUP BY 1 ORDER BY SUM(ce.usd) DESC`, orgID, since)
	if err != nil {
		return rep, err
	}
	defer mrows.Close()
	for mrows.Next() {
		var mc ModelCost
		if err := mrows.Scan(&mc.Model, &mc.TotalUSD, &mc.Input, &mc.Output, &mc.CacheRead, &mc.CacheCreation, &mc.Entries); err != nil {
			return rep, err
		}
		rep.Models = append(rep.Models, mc)
	}
	return rep, mrows.Err()
}

// --- Approval queue ---

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

// DecideApproval decides a pending gate; idempotent against double clicks.
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
