// Package audit keeps the trail of administrative actions: who touched what on
// the platform, and when.
//
// The separation from the recording (internal/observability) is one of content:
// there it says what the AGENTS do — gapless, with screenshots, attached to the
// task. Here it says what HUMANS do to the platform. Only both together yield
// the traceability Covey sets out to deliver (spec/06): without this half,
// somebody could delete a guard-rail, let the agent do its work and create the
// rule again afterwards — and the recording would show a flawless run.
package audit

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Eintrag is a recorded action.
//
// No request body: it would contain secret values and passwords. An audit trail
// that cannot be kept because secrets sit inside it is none.
type Eintrag struct {
	ID         int64      `json:"id"`
	OrgID      uuid.UUID  `json:"org_id"`
	ActorID    *uuid.UUID `json:"actor_id,omitempty"`
	ActorEmail string     `json:"actor_email"`
	ActorRole  string     `json:"actor_role"`
	Method     string     `json:"method"`
	Path       string     `json:"path"`
	Status     int        `json:"status"`
	ClientIP   string     `json:"client_ip,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Record puts an action on record.
func (s *Store) Record(ctx context.Context, e Eintrag) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO audit_log
		(org_id, actor_id, actor_email, actor_role, method, path, status, client_ip)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		e.OrgID, e.ActorID, e.ActorEmail, e.ActorRole, e.Method, e.Path, e.Status, e.ClientIP)
	return err
}

// List returns the most recent entries of an organization.
func (s *Store) List(ctx context.Context, orgID uuid.UUID, limit int) ([]Eintrag, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `SELECT id, org_id, actor_id, actor_email, actor_role,
		method, path, status, client_ip, created_at
		FROM audit_log WHERE org_id=$1 ORDER BY id DESC LIMIT $2`, orgID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Eintrag{}
	for rows.Next() {
		var e Eintrag
		if err := rows.Scan(&e.ID, &e.OrgID, &e.ActorID, &e.ActorEmail, &e.ActorRole,
			&e.Method, &e.Path, &e.Status, &e.ClientIP, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Cleanup deletes entries older than age. An audit trail grows without bound
// otherwise; how long it is kept is a compliance question and therefore belongs
// to the operator, not to this code.
func (s *Store) Cleanup(ctx context.Context, age time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM audit_log WHERE created_at < now() - $1::interval", age.String())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
