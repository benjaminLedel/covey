// Package store hält das Request-Log in Postgres (Tabelle request_log). Es ist
// die Control-Plane-Seite von internal/reqlog: der Kern erzeugt Einträge
// (auch in der Sandbox), dieser Store schreibt, liest und prunt sie.
//
// Geschrieben wird asynchron über einen gepufferten Kanal: ein Request-Pfad
// darf nie an der Diagnose hängen. Läuft der Puffer voll, werden Einträge
// verworfen und gezählt — Diagnose-Daten sind kein Audit-Trail.
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

// bufferSize ist die Puffertiefe des Schreib-Kanals. Großzügig genug für
// Lastspitzen, klein genug, um bei einer hängenden DB nicht zu wachsen.
const bufferSize = 512

// pruneInterval ist der Takt des Aufräumens (Retention + harte Obergrenze).
const pruneInterval = 15 * time.Minute

// MaxRows ist die harte Obergrenze der Tabelle — sie greift auch, wenn in
// kurzer Zeit sehr viel Verkehr anfällt und die Alters-Retention noch nicht.
const MaxRows = 50_000

// Record ist ein Eintrag mit dem Kontext, den nur die Control Plane kennt.
type Record struct {
	reqlog.Entry
	OrgID   *uuid.UUID `json:"org_id,omitempty"`
	AgentID *uuid.UUID `json:"agent_id,omitempty"`
	TaskID  *uuid.UUID `json:"task_id,omitempty"`
}

// View ist die Lese-Sicht eines Eintrags (API/UI).
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

// Filter schränkt die Liste ein (alles optional).
type Filter struct {
	Direction string
	System    string
	AgentID   *uuid.UUID
	OnlyBad   bool   // nur Fehler: Status >= 400 oder Transport-Fehler
	Query     string // Teilstring in URL/Fehler
	BeforeID  int64  // Paginierung: nur Einträge älter als diese ID
	Limit     int
}

// Store schreibt und liest das Request-Log.
type Store struct {
	pool *pgxpool.Pool
	log  *slog.Logger

	ch chan Record
	// Bodies steuert, ob Request-/Response-Ausschnitte gespeichert werden.
	// false = nur Metadaten (COVEY_REQUEST_LOG_BODIES=false).
	bodies bool
	// Retention ist das Alter, ab dem Einträge verschwinden.
	retention time.Duration

	dropped atomic.Int64
}

// NewStore baut den Store. bodies=false speichert nur Metadaten; retention <= 0
// fällt auf 72h zurück.
func NewStore(pool *pgxpool.Pool, log *slog.Logger, bodies bool, retention time.Duration) *Store {
	if retention <= 0 {
		retention = 72 * time.Hour
	}
	if log == nil {
		log = slog.Default()
	}
	return &Store{pool: pool, log: log, ch: make(chan Record, bufferSize), bodies: bodies, retention: retention}
}

// Sink liefert die reqlog.Sink dieses Stores — für reqlog.SetDefault bzw. für
// Context-Senken mit Agenten-Bezug.
func (s *Store) Sink(orgID, agentID, taskID *uuid.UUID) reqlog.Sink {
	return func(e reqlog.Entry) {
		s.Enqueue(Record{Entry: e, OrgID: orgID, AgentID: agentID, TaskID: taskID})
	}
}

// Enqueue nimmt einen Eintrag zum Schreiben an. Nicht blockierend: ist der
// Puffer voll, wird verworfen (und gezählt).
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

// Dropped ist die Zahl der wegen vollem Puffer verworfenen Einträge.
func (s *Store) Dropped() int64 { return s.dropped.Load() }

// Run schreibt den Puffer in die Datenbank und prunt periodisch. Blockiert bis
// ctx endet (als Goroutine starten).
func (s *Store) Run(ctx context.Context) {
	ticker := time.NewTicker(pruneInterval)
	defer ticker.Stop()
	// Beim Start einmal aufräumen — nach einem Neustart kann das Log alt sein.
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
		s.log.Debug("request-log schreiben", "err", err)
	}
}

// prune löscht nach Alter und kappt anschließend auf MaxRows.
func (s *Store) prune(ctx context.Context) {
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM request_log WHERE created_at < now() - $1::interval`,
		s.retention.String()); err != nil && ctx.Err() == nil {
		s.log.Debug("request-log pruning (alter)", "err", err)
	}
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM request_log WHERE id <= (
			SELECT id FROM request_log ORDER BY id DESC OFFSET $1 LIMIT 1)`,
		MaxRows); err != nil && ctx.Err() == nil {
		s.log.Debug("request-log pruning (obergrenze)", "err", err)
	}
}

// List liefert Einträge, neueste zuerst, ohne Bodies (die kommen im Detail).
// Sichtbar sind die Einträge der eigenen Organisation plus die ohne
// Organisations-Bezug — ein abgelehnter Webhook hat keine.
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

// Get liefert einen Eintrag samt Bodies, org-gescopt wie List.
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
	// pgx.ErrNoRows reicht durch — mapErr macht daraus ein 404.
	v.BodiesShown = s.bodies
	return v, err
}

// Systems liefert die im Log vorkommenden Zielsystem-Namen — die UI leitet
// ihren Filter daraus ab, statt eine eigene Liste zu führen.
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

// Clear leert das Log der Organisation (inkl. der Einträge ohne Org-Bezug).
func (s *Store) Clear(ctx context.Context, orgID uuid.UUID) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM request_log WHERE org_id IS NULL OR org_id = $1`, orgID)
	return tag.RowsAffected(), err
}

// BodiesEnabled meldet, ob Bodies gespeichert werden (für die UI-Erklärung).
func (s *Store) BodiesEnabled() bool { return s != nil && s.bodies }

// Retention ist das konfigurierte Aufbewahrungsfenster.
func (s *Store) Retention() time.Duration { return s.retention }
