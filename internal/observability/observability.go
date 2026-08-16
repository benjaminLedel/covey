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

// CredentialCost is what one credential of a runtime paid for — the breakdown
// that answers "is one seat too few, or one too many". Kind says what the money
// column means there: real billing on a metered credential, a list-price
// equivalent on a subscription seat (spec/17).
//
// Runs from before the pools carry no attribution and are missing here; they
// still count towards the totals. The view has to be able to say that, so that
// a gap does not read as "this value cost nothing".
type CredentialCost struct {
	RuntimeID   uuid.UUID `json:"runtime_id"`
	RuntimeName string    `json:"runtime_name"`
	Ord         int       `json:"ord"`
	Label       string    `json:"label"`
	Kind        string    `json:"kind"`
	TotalUSD    float64   `json:"total_usd"`
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
	// PrevCount and PrevUnitUSD are the same figures for the equally long
	// period directly before — the trend. Deliberately the raw values rather
	// than a computed percentage: the direction is not the same kind of news
	// for both. A falling unit cost is an improvement; twice the tickets can be
	// twice the delivery or twice the incoming mail, and only the reader knows
	// which. So the display colours the price and leaves the count neutral.
	PrevCount   int64    `json:"prev_count"`
	PrevUnitUSD *float64 `json:"prev_unit_usd,omitempty"`
	// Series is the course over the period in fixed buckets (sparkline). With
	// `je:` the buckets do not add up to Count — the same object in two buckets
	// counts in both.
	Series []int64 `json:"series,omitempty"`
	// Returned are the objects among Count that a SECOND run had to touch
	// again — the rework rate, and the figure that keeps the price honest: a
	// ticket resolved today and reopened on Thursday was not resolved, and it
	// counts as delivery all the same.
	//
	// Only available with `je:`, because it needs the object identity. Without
	// it the field stays 0 and the display leaves it out — an invented zero
	// would claim a quality that was never measured.
	Returned int64 `json:"returned,omitempty"`
}

// minCountForUnitCost is the number of events from which a unit cost is a
// measurement rather than an accident.
const minCountForUnitCost = 5

// Quality are the figures that qualify a price (spec/17-kpis.md). A price says
// what a result cost, not whether the result was any good.
//
// All three are proxies, and none of them settles the question: a case that
// never comes back may have been resolved well or merely abandoned by a
// resigned reporter. They are worth having because they are cheap and because
// they move in the right direction.
type Quality struct {
	// Decided are the approval gates a human decided, Denied those refused.
	// The only figure here that is not a proxy: somebody looked at a proposed
	// action and said no.
	Decided int64 `json:"decided"`
	Denied  int64 `json:"denied"`
	// ResponseSeconds is the MEDIAN time from the incoming event to the run's
	// first action — the value that has nothing to do with money and is often
	// the actual one. Median, not mean: one run that hung for six hours must
	// not colour the picture. Nil when nothing is measurable in the period.
	ResponseSeconds *float64 `json:"response_seconds,omitempty"`
}

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
	// Credentials is the breakdown per pool value. Empty as long as nobody
	// keeps several values under one key — then there is nothing to break down.
	Credentials []CredentialCost `json:"credentials"`
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
	// Which ids answer the question is decided first, and on its own. That
	// separation is the whole point.
	//
	// Asked in one statement, the planner walks the primary key backwards and
	// filters by agent on the way. It picks that because it assumes an agent's
	// rows are spread evenly across the ids — cheap for one that worked a minute
	// ago, because its rows sit at the very end. For one that has been idle it
	// discards every foreign row in between: measured on an installation with
	// 262,000 events, 34,784 rows thrown away, 7,658 buffers, 16 ms instead of
	// 1. That is why a log opens instantly for one agent and slowly for the
	// next, and the gap widens with every event anybody writes.
	//
	// Ordering by (agent_id, id) does not help: with agent_id pinned to one
	// value the planner recognises the leading column as redundant and drops it.
	// Selecting only the id does help, because then idx_recording_agent covers
	// the question completely and the primary key does not. MATERIALIZED keeps
	// the planner from folding the two halves back together. Same plan, same
	// rows, 1 ms — and the scan starts where the answer is instead of where the
	// table ends.
	const spalten = `e.id, e.org_id, e.agent_id, e.task_id, e.kind, e.payload, e.created_at`

	// The scope is one equality rather than the previous "$2 IS NULL OR
	// task_id = $2": that OR is not a range the planner can start from, and it
	// ruled out the task index for exactly the same reason.
	wo := `WHERE agent_id = $1 AND id > $2`
	args := []any{agentID, afterID, limit}
	if taskID != nil {
		wo = `WHERE task_id = $1 AND agent_id = $4 AND id > $2`
		args = []any{*taskID, afterID, limit, agentID}
	}
	// Without an after cursor the most recent happenings are what matters: the
	// last N events, sorted chronologically. With a cursor (live follow) still
	// forwards from the known ID.
	richtung := "DESC"
	if afterID > 0 {
		richtung = "ASC"
	}
	query := `WITH treffer AS MATERIALIZED (
			SELECT id FROM recording_events ` + wo + `
			ORDER BY id ` + richtung + ` LIMIT $3
		)
		SELECT ` + spalten + ` FROM recording_events e JOIN treffer USING (id) ORDER BY e.id`
	rows, err := s.pool.Query(ctx, query, args...)
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

// ActionSubjectsSince lists the distinct subjects of the actions an agent has
// executed SUCCESSFULLY since `since` (e.g. "gitlab:comment_mr").
//
// Deliberately over a period and not over a single task: the caller — the
// heartbeat watermark — asks about a whole run, and a run can span several
// tasks (a continuation carries on where the turn limit cut off). Anchored on
// the task it would only see the last segment of the chain and take the comment
// from the first one for foreign activity.
//
// Failed actions do not count: what did not go through cannot have changed
// anything in the target system.
func (s *Store) ActionSubjectsSince(ctx context.Context, agentID uuid.UUID, since time.Time) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT payload->>'action'
		FROM recording_events
		WHERE agent_id=$1 AND kind='action' AND created_at >= $2
		  AND payload->>'ok' = 'true' AND payload->>'action' IS NOT NULL`, agentID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var subject string
		if err := rows.Scan(&subject); err != nil {
			return nil, err
		}
		out = append(out, subject)
	}
	return out, rows.Err()
}

// AddCost books costs from a cost event of the daemon.
//
// runtimeID/ord say WHICH credential paid for the run — the one the capacity
// layer picked for it (spec/18). A nil runtime means: not attributable, and the
// entry then counts towards the totals but not towards the breakdown per
// credential. That is the case for every run from before the runtimes.
func (s *Store) AddCost(ctx context.Context, agentID uuid.UUID, taskID *uuid.UUID, usd float64, tok Tokens, model string, runtimeID uuid.UUID, ord int) error {
	var (
		rt  *uuid.UUID
		crd *int
	)
	if runtimeID != uuid.Nil {
		rt, crd = &runtimeID, &ord
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO cost_entries
		(agent_id, task_id, usd, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, model, runtime_id, credential_ord)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		agentID, taskID, usd, tok.Input, tok.Output, tok.CacheRead, tok.CacheCreation, model, rt, crd)
	return err
}

// CredentialUsage is what one credential of a runtime consumed in the rolling
// window — the measurement behind its limit (runtimes.UsageFunc).
//
// The token figure is the sum of all four kinds. For an API key the USD column
// is the honest one (it is real billing); for a subscription token USD is
// notional and the token count is the closest available proxy for the
// provider's own rolling window. Neither is the provider's actual counter —
// they steer, and the hard signal (a rejected credential) corrects them.
func (s *Store) CredentialUsage(ctx context.Context, runtimeID uuid.UUID, ord int, window time.Duration) (float64, int64, error) {
	var (
		usd    float64
		tokens int64
	)
	// No org filter needed here: a runtime belongs to exactly one organisation
	// (spec/18, D13), so its id already scopes the query.
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(usd),0),
			COALESCE(SUM(input_tokens + output_tokens + cache_read_tokens + cache_creation_tokens),0)
		FROM cost_entries
		WHERE runtime_id=$1 AND credential_ord=$2 AND created_at >= $3`,
		runtimeID, ord, time.Now().Add(-window)).Scan(&usd, &tokens)
	return usd, tokens, err
}

// SlotConsumption is one value's consumption for the pool view.
type SlotConsumption struct {
	Ord    int     `json:"ord"`
	USD    float64 `json:"usd"`
	Tokens int64   `json:"tokens"`
	Runs   int64   `json:"runs"`
}

// RuntimeUsage is CredentialUsage for every credential of a runtime in one go —
// the figures behind the utilisation bars. Credentials without any consumption
// in the window are missing from the result; that is the honest state and not
// the same as zero (capacity nobody has been sitting on has not consumed
// nothing, it has not been asked).
func (s *Store) RuntimeUsage(ctx context.Context, runtimeID uuid.UUID, window time.Duration) ([]SlotConsumption, error) {
	rows, err := s.pool.Query(ctx, `SELECT credential_ord, COALESCE(SUM(usd),0),
			COALESCE(SUM(input_tokens + output_tokens + cache_read_tokens + cache_creation_tokens),0),
			COUNT(*)
		FROM cost_entries
		WHERE runtime_id=$1 AND credential_ord IS NOT NULL AND created_at >= $2
		GROUP BY credential_ord ORDER BY credential_ord`,
		runtimeID, time.Now().Add(-window))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SlotConsumption{}
	for rows.Next() {
		var c SlotConsumption
		if err := rows.Scan(&c.Ord, &c.USD, &c.Tokens, &c.Runs); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
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
// The second return value is the rework count: objects that a SECOND run had to
// take up again. It is only available with `je:` — without an object identity
// there is nothing to recognise a returning case by.
//
// How it is recognised: two DIFFERENT tasks acting on the same object. The
// obvious route — the task's correlation_key — does not work, because `Complete`
// clears the key when a task finishes; a completed task no longer knows which
// ticket it was. The action parameters do know, they are the same identity the
// indicator counts on, and the signal is exactly the intended one: one case,
// two runs.
// The third return value is the count of the SAME LENGTH of time directly
// before `since` — the comparison a trend needs. It travels in the same query
// (a FILTER over a doubled window) rather than in a second one: two queries
// would read the same index twice for one number.
func (s *Store) CountIndicator(ctx context.Context, ind Indicator, agentIDs []uuid.UUID, since time.Time) (int64, int64, int64, error) {
	if len(agentIDs) == 0 {
		return 0, 0, 0, nil
	}
	// The previous period: the same span again, directly before.
	vor := since.Add(-time.Since(since))
	var n, returned, prev int64
	if ind.Action == "" {
		// The task form: a backlog task that reached `done`. updated_at is the
		// moment of the last transition, which for a terminal task is when it
		// got there.
		err := s.pool.QueryRow(ctx, `SELECT
			COUNT(*) FILTER (WHERE updated_at >= $2),
			COUNT(*) FILTER (WHERE updated_at < $2)
			FROM backlog_tasks
			WHERE agent_id = ANY($1) AND state = 'done' AND updated_at >= $4
			  AND ($3 = '' OR origin = $3)`, agentIDs, since, ind.Origin, vor).Scan(&n, &prev)
		return n, 0, prev, err
	}

	// A wildcard cannot use the index as a range scan (the collation decides
	// how prefixes compare), so it scans the partial index. Acceptable: the
	// form exists for the coarse "did it touch GitLab at all" and is rare.
	match, arg := "payload->>'action' = $3", ind.Action
	if strings.HasSuffix(ind.Action, ":*") {
		match, arg = "payload->>'action' LIKE $3", strings.TrimSuffix(ind.Action, "*")+"%"
	}
	// The window spans BOTH periods; the FILTERs split them apart.
	where := `FROM recording_events
		WHERE agent_id = ANY($1) AND kind = 'action' AND created_at >= $4
		  AND ` + match + ` AND payload->>'ok' = 'true'`

	if ind.Per == "" {
		err := s.pool.QueryRow(ctx, `SELECT
			COUNT(*) FILTER (WHERE created_at >= $2),
			COUNT(*) FILTER (WHERE created_at < $2) `+where,
			agentIDs, since, arg, vor).Scan(&n, &prev)
		return n, 0, prev, err
	}
	// With `je:` both periods count objects, and an object that appears in both
	// counts in both — that is intended: it was worked on in both periods.
	obj := "payload->'params'->>'" + sanitizeParam(ind.Per) + "'"
	err := s.pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE jetzt > 0),
			COUNT(*) FILTER (WHERE laeufe > 1 AND jetzt > 0),
			COUNT(*) FILTER (WHERE vorher > 0)
		FROM (
			SELECT COUNT(DISTINCT task_id) FILTER (WHERE created_at >= $2) AS laeufe,
			       COUNT(*) FILTER (WHERE created_at >= $2) AS jetzt,
			       COUNT(*) FILTER (WHERE created_at < $2) AS vorher
			`+where+` AND `+obj+` IS NOT NULL
			GROUP BY `+obj+`
		) x`, agentIDs, since, arg, vor).Scan(&n, &returned, &prev)
	return n, returned, prev, err
}

// IndicatorSeries is the course of an indicator over the period, in a fixed
// number of buckets — the sparkline next to the figure.
//
// Fixed buckets rather than days or hours, because the sparkline has a fixed
// width: a 24-hour window and a 90-day one have to produce the same number of
// points, or the same amount of pixels would mean something different per
// period. width_bucket does the division in the database; gaps become zeros
// here, since a period without work is a real value and not a missing one.
//
// The bucket values are NOT summable to the total when `je:` is set — the same
// ticket in two buckets counts in both. That is right for a course ("how much
// was going on then") and wrong for a total, which is why the total comes from
// CountIndicator and not from this.
func (s *Store) IndicatorSeries(ctx context.Context, ind Indicator, agentIDs []uuid.UUID, since time.Time, buckets int) ([]int64, error) {
	if len(agentIDs) == 0 || buckets <= 0 {
		return nil, nil
	}
	out := make([]int64, buckets)
	von, bis := float64(since.Unix()), float64(time.Now().Unix())
	if bis <= von {
		return out, nil
	}

	var query string
	var args []any
	if ind.Action == "" {
		query = `SELECT width_bucket(EXTRACT(EPOCH FROM updated_at), $4, $5, $6), COUNT(*)
			FROM backlog_tasks
			WHERE agent_id = ANY($1) AND state = 'done' AND updated_at >= $2
			  AND ($3 = '' OR origin = $3)
			GROUP BY 1`
		args = []any{agentIDs, since, ind.Origin, von, bis, buckets}
	} else {
		match, arg := "payload->>'action' = $3", ind.Action
		if strings.HasSuffix(ind.Action, ":*") {
			match, arg = "payload->>'action' LIKE $3", strings.TrimSuffix(ind.Action, "*")+"%"
		}
		zaehl := "COUNT(*)"
		if ind.Per != "" {
			zaehl = "COUNT(DISTINCT payload->'params'->>'" + sanitizeParam(ind.Per) + "')"
		}
		query = `SELECT width_bucket(EXTRACT(EPOCH FROM created_at), $4, $5, $6), ` + zaehl + `
			FROM recording_events
			WHERE agent_id = ANY($1) AND kind = 'action' AND created_at >= $2
			  AND ` + match + ` AND payload->>'ok' = 'true'
			GROUP BY 1`
		args = []any{agentIDs, since, arg, von, bis, buckets}
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var b int
		var n int64
		if err := rows.Scan(&b, &n); err != nil {
			return nil, err
		}
		// width_bucket answers 0 below and buckets+1 above the range; the last
		// bucket takes the overflow, which is the events of the current second.
		if b > buckets {
			b = buckets
		}
		if b >= 1 {
			out[b-1] += n
		}
	}
	return out, rows.Err()
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

// CostOfAgentsBetween is the same for a closed window — the denominator of the
// previous period. Without it a trend in unit cost would divide the old count
// by today's cost and report a change that never happened.
func (s *Store) CostOfAgentsBetween(ctx context.Context, agentIDs []uuid.UUID, since, until time.Time) (float64, error) {
	if len(agentIDs) == 0 {
		return 0, nil
	}
	var usd float64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(usd),0) FROM cost_entries
		WHERE agent_id = ANY($1) AND created_at >= $2 AND created_at < $3`,
		agentIDs, since, until).Scan(&usd)
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

// QualityReport collects the scope-wide figures that qualify the prices: the
// human verdict at the approval gates, and how fast the first reaction was.
//
// The rework rate is NOT here — it hangs off the indicator, see CountIndicator.
func (s *Store) QualityReport(ctx context.Context, agentIDs []uuid.UUID, since time.Time) (Quality, error) {
	var q Quality
	if len(agentIDs) == 0 {
		return q, nil
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE status = 'denied')
		FROM approvals
		WHERE agent_id = ANY($1) AND status <> 'pending' AND decided_at >= $2`,
		agentIDs, since).Scan(&q.Decided, &q.Denied); err != nil {
		return q, err
	}
	// The first action of a run against the moment its task appeared.
	err := s.pool.QueryRow(ctx, `
		SELECT PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (erste - angelegt)))
		FROM (
			SELECT t.created_at AS angelegt, MIN(e.created_at) AS erste
			FROM backlog_tasks t
			JOIN recording_events e ON e.task_id = t.id AND e.kind = 'action'
			WHERE t.agent_id = ANY($1) AND t.created_at >= $2
			GROUP BY t.id, t.created_at
		) x`, agentIDs, since).Scan(&q.ResponseSeconds)
	return q, err
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
	rep := OrgCostReport{Bucket: normalizeBucket(bucket), Series: []CostBucket{}, Agents: []AgentCost{},
		Models: []ModelCost{}, Credentials: []CredentialCost{}}

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
	func() {
		defer mrows.Close()
		for mrows.Next() {
			var mc ModelCost
			if err = mrows.Scan(&mc.Model, &mc.TotalUSD, &mc.Input, &mc.Output, &mc.CacheRead, &mc.CacheCreation, &mc.Entries); err != nil {
				return
			}
			rep.Models = append(rep.Models, mc)
		}
		err = mrows.Err()
	}()
	if err != nil {
		return rep, err
	}

	// Per credential. The label comes from the runtime credential; one deleted
	// in the meantime leaves its costs standing (LEFT JOIN) — they were
	// incurred, and dropping them here would quietly shrink the sum against the
	// totals above.
	crows, err := s.pool.Query(ctx, `SELECT ce.runtime_id, ce.credential_ord,
		COALESCE(r.display_name,''), COALESCE(rc.label,''), COALESCE(rc.kind,''),
		COALESCE(SUM(ce.usd),0), `+tokenSums("ce.")+`, COUNT(*)
		FROM cost_entries ce
		JOIN agents a ON a.id=ce.agent_id
		LEFT JOIN runtimes r ON r.id=ce.runtime_id
		LEFT JOIN runtime_credentials rc
			ON rc.runtime_id=ce.runtime_id AND rc.ord=ce.credential_ord
		WHERE a.org_id=$1 AND ce.created_at >= $2 AND ce.runtime_id IS NOT NULL
		GROUP BY ce.runtime_id, ce.credential_ord, r.display_name, rc.label, rc.kind
		ORDER BY SUM(ce.usd) DESC`, orgID, since)
	if err != nil {
		return rep, err
	}
	defer crows.Close()
	for crows.Next() {
		var cc CredentialCost
		if err := crows.Scan(&cc.RuntimeID, &cc.Ord, &cc.RuntimeName, &cc.Label, &cc.Kind,
			&cc.TotalUSD, &cc.Input, &cc.Output, &cc.CacheRead, &cc.CacheCreation,
			&cc.Entries); err != nil {
			return rep, err
		}
		rep.Credentials = append(rep.Credentials, cc)
	}
	return rep, crows.Err()
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
