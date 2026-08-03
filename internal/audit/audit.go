// Package audit führt die Spur der Verwaltungshandlungen: wer wann was an der
// Plattform angefasst hat.
//
// Die Trennung zum Recording (internal/observability) ist inhaltlich: Dort
// steht, was die AGENTEN tun — lückenlos, mit Screenshots, an der Aufgabe
// hängend. Hier steht, was MENSCHEN an der Plattform tun. Beides zusammen
// ergibt erst die Nachvollziehbarkeit, mit der Covey antritt (spec/06): Ohne
// diese Hälfte könnte jemand eine Guard-Rail löschen, den Agenten seine Arbeit
// tun lassen und die Regel danach wieder anlegen — im Recording stünde ein
// tadelloser Lauf.
package audit

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Eintrag ist eine festgehaltene Handlung.
//
// Kein Request-Body: In ihm stünden Secret-Werte und Passwörter. Eine
// Audit-Spur, die man nicht aufbewahren kann, weil Geheimnisse darin liegen,
// ist keine.
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

// Record hält eine Handlung fest.
func (s *Store) Record(ctx context.Context, e Eintrag) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO audit_log
		(org_id, actor_id, actor_email, actor_role, method, path, status, client_ip)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		e.OrgID, e.ActorID, e.ActorEmail, e.ActorRole, e.Method, e.Path, e.Status, e.ClientIP)
	return err
}

// List liefert die jüngsten Einträge einer Organisation.
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

// Cleanup löscht Einträge älter als age. Eine Audit-Spur wächst sonst
// unbegrenzt; wie lange sie vorgehalten wird, ist eine Compliance-Frage und
// gehört deshalb dem Betreiber, nicht diesem Code.
func (s *Store) Cleanup(ctx context.Context, age time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM audit_log WHERE created_at < now() - $1::interval", age.String())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
