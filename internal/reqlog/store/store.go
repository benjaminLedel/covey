// Package store keeps the request log in Postgres (table request_log). It is
// the control-plane side of internal/reqlog: the core produces entries (in the
// sandbox too), this store writes, reads and prunes them.
//
// Writing happens asynchronously through a buffered channel: a request path
// must never hang on diagnostics. If the buffer runs full, entries are dropped
// and counted — diagnostic data is not an audit trail.
package store

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/reqlog"
)

// bufferSize is the buffer depth of the write channel. Generous enough for load
// spikes, small enough not to grow when the DB hangs.
const bufferSize = 512

// pruneInterval is the cadence of the cleanup (retention + hard upper bound).
const pruneInterval = 15 * time.Minute

// MaxRows is the hard upper bound of the table — it also applies when a lot of
// traffic accumulates in a short time and the age-based retention does not yet.
const MaxRows = 50_000

// Record is an entry with the context only the control plane knows.
type Record struct {
	reqlog.Entry
	OrgID   *uuid.UUID `json:"org_id,omitempty"`
	AgentID *uuid.UUID `json:"agent_id,omitempty"`
	TaskID  *uuid.UUID `json:"task_id,omitempty"`
}

// View is the read view of an entry (API/UI).
type View struct {
	ID          int64      `json:"id"`
	CreatedAt   time.Time  `json:"created_at"`
	OrgID       *uuid.UUID `json:"org_id,omitempty"`
	AgentID     *uuid.UUID `json:"agent_id,omitempty"`
	AgentSlug   string     `json:"agent_slug,omitempty"`
	TaskID      *uuid.UUID `json:"task_id,omitempty"`
	Direction   string     `json:"direction"`
	System      string     `json:"system"`
	Method      string     `json:"method"`
	URL         string     `json:"url"`
	Status      int        `json:"status"`
	DurationMS  int64      `json:"duration_ms"`
	ReqBytes    int64      `json:"req_bytes"`
	RespBytes   int64      `json:"resp_bytes"`
	Error       string     `json:"error,omitempty"`
	Remote      string     `json:"remote,omitempty"`
	ReqBody     string     `json:"req_body,omitempty"`
	RespBody    string     `json:"resp_body,omitempty"`
	BodiesShown bool       `json:"bodies_shown"`
}

// Filter narrows the list down (everything optional).
type Filter struct {
	Direction string
	System    string
	AgentID   *uuid.UUID
	OnlyBad   bool   // errors only: status >= 400 or transport error
	Query     string // substring in URL/error
	BeforeID  int64  // pagination: only entries older than this ID
	Limit     int
}

// Store writes and reads the request log.
type Store struct {
	pool *pgxpool.Pool
	log  *slog.Logger

	ch chan Record
	// bodies controls whether request/response excerpts are stored.
	// false = metadata only (COVEY_REQUEST_LOG_BODIES=false).
	bodies bool
	// retention is the age from which entries disappear.
	retention time.Duration

	dropped atomic.Int64
}

// NewStore builds the store. bodies=false stores metadata only; retention <= 0
// falls back to 72h.
func NewStore(pool *pgxpool.Pool, log *slog.Logger, bodies bool, retention time.Duration) *Store {
	if retention <= 0 {
		retention = 72 * time.Hour
	}
	if log == nil {
		log = slog.Default()
	}
	return &Store{pool: pool, log: log, ch: make(chan Record, bufferSize), bodies: bodies, retention: retention}
}

// Sink returns this store's reqlog.Sink — for reqlog.SetDefault and for context
// sinks with an agent reference.
func (s *Store) Sink(orgID, agentID, taskID *uuid.UUID) reqlog.Sink {
	return func(e reqlog.Entry) {
		s.Enqueue(Record{Entry: e, OrgID: orgID, AgentID: agentID, TaskID: taskID})
	}
}

// Enqueue accepts an entry for writing. Non-blocking: if the buffer is full it
// is dropped (and counted).
func (s *Store) Enqueue(rec Record) {
	if s == nil {
		return
	}
	if !s.bodies {
		rec.ReqBody, rec.RespBody = "", ""
	}
	select {
	case s.ch <- rec:
	default:
		s.dropped.Add(1)
	}
}

// Dropped is the number of entries dropped because the buffer was full.
func (s *Store) Dropped() int64 { return s.dropped.Load() }

// Run writes the buffer into the database and prunes periodically. Blocks until
// ctx ends (start it as a goroutine).
func (s *Store) Run(ctx context.Context) {
	ticker := time.NewTicker(pruneInterval)
	defer ticker.Stop()
	// Clean up once at start — after a restart the log can be stale.
	s.prune(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case rec := <-s.ch:
			s.insert(ctx, rec)
		case <-ticker.C:
			s.prune(ctx)
		}
	}
}

func (s *Store) insert(ctx context.Context, rec Record) {
	created := rec.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO request_log
		(created_at, org_id, agent_id, task_id, direction, system, method, url,
		 status, duration_ms, req_bytes, resp_bytes, req_body, resp_body, error, remote)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		created, rec.OrgID, rec.AgentID, rec.TaskID, rec.Direction, rec.System,
		rec.Method, rec.URL, rec.Status, rec.DurationMS, rec.ReqBytes, rec.RespBytes,
		rec.ReqBody, rec.RespBody, rec.Error, rec.Remote)
	if err != nil && ctx.Err() == nil {
		s.log.Debug("writing request log", "err", err)
	}
}

// prune deletes by age and afterwards caps at MaxRows.
func (s *Store) prune(ctx context.Context) {
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM request_log WHERE created_at < now() - $1::interval`,
		s.retention.String()); err != nil && ctx.Err() == nil {
		s.log.Debug("request-log pruning (age)", "err", err)
	}
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM request_log WHERE id <= (
			SELECT id FROM request_log ORDER BY id DESC OFFSET $1 LIMIT 1)`,
		MaxRows); err != nil && ctx.Err() == nil {
		s.log.Debug("request-log pruning (upper bound)", "err", err)
	}
}

// List returns entries, newest first, without bodies (those come with the
// detail view). Visible are the entries of one's own organization plus those
// without an organization reference — a rejected webhook has none.
func (s *Store) List(ctx context.Context, orgID uuid.UUID, f Filter) ([]View, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	q := `SELECT l.id, l.created_at, l.org_id, l.agent_id, COALESCE(a.slug,''), l.task_id,
	             l.direction, l.system, l.method, l.url, l.status, l.duration_ms,
	             l.req_bytes, l.resp_bytes, l.error, l.remote
	      FROM request_log l LEFT JOIN agents a ON a.id = l.agent_id
	      WHERE (l.org_id IS NULL OR l.org_id = $1)
	        AND ($2 = '' OR l.direction = $2)
	        AND ($3 = '' OR l.system = $3)
	        AND ($4::uuid IS NULL OR l.agent_id = $4)
	        AND ($5 = false OR l.status >= 400 OR l.status = 0 OR l.error <> '')
	        AND ($6 = '' OR l.url ILIKE '%' || $6 || '%' OR l.error ILIKE '%' || $6 || '%')
	        AND ($7 = 0 OR l.id < $7)
	      ORDER BY l.id DESC LIMIT $8`
	rows, err := s.pool.Query(ctx, q, orgID, f.Direction, f.System, f.AgentID,
		f.OnlyBad, strings.TrimSpace(f.Query), f.BeforeID, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []View{}
	for rows.Next() {
		var v View
		if err := rows.Scan(&v.ID, &v.CreatedAt, &v.OrgID, &v.AgentID, &v.AgentSlug, &v.TaskID,
			&v.Direction, &v.System, &v.Method, &v.URL, &v.Status, &v.DurationMS,
			&v.ReqBytes, &v.RespBytes, &v.Error, &v.Remote); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Get returns an entry including bodies, org-scoped like List.
func (s *Store) Get(ctx context.Context, orgID uuid.UUID, id int64) (View, error) {
	var v View
	err := s.pool.QueryRow(ctx, `SELECT l.id, l.created_at, l.org_id, l.agent_id, COALESCE(a.slug,''), l.task_id,
	        l.direction, l.system, l.method, l.url, l.status, l.duration_ms,
	        l.req_bytes, l.resp_bytes, l.error, l.remote, l.req_body, l.resp_body
	      FROM request_log l LEFT JOIN agents a ON a.id = l.agent_id
	      WHERE l.id = $2 AND (l.org_id IS NULL OR l.org_id = $1)`, orgID, id).
		Scan(&v.ID, &v.CreatedAt, &v.OrgID, &v.AgentID, &v.AgentSlug, &v.TaskID,
			&v.Direction, &v.System, &v.Method, &v.URL, &v.Status, &v.DurationMS,
			&v.ReqBytes, &v.RespBytes, &v.Error, &v.Remote, &v.ReqBody, &v.RespBody)
	// pgx.ErrNoRows passes through — mapErr turns it into a 404.
	v.BodiesShown = s.bodies
	return v, err
}

// Systems returns the target-system names occurring in the log — the UI derives
// its filter from that instead of keeping a list of its own.
func (s *Store) Systems(ctx context.Context, orgID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT system FROM request_log
		WHERE (org_id IS NULL OR org_id = $1) AND system <> '' ORDER BY system`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Clear empties the organization's log (including the entries without an org
// reference).
func (s *Store) Clear(ctx context.Context, orgID uuid.UUID) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM request_log WHERE org_id IS NULL OR org_id = $1`, orgID)
	return tag.RowsAffected(), err
}

// BodiesEnabled reports whether bodies are stored (for the UI's explanation).
func (s *Store) BodiesEnabled() bool { return s != nil && s.bodies }

// Retention is the configured retention window.
func (s *Store) Retention() time.Duration { return s.retention }
