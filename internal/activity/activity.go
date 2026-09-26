// Package activity keeps a person's activity log (#363): sessions of work
// the macOS app recorded in the background, private to the seat, and the
// daily review written from them.
package activity

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Session is one stretch in one app, window and page.
type Session struct {
	ID        uuid.UUID `json:"id"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	App       string    `json:"app"`
	Bundle    string    `json:"bundle"`
	Window    string    `json:"window"`
	URL       string    `json:"url"`
	Field     string    `json:"field"`
	Excerpt   string    `json:"excerpt"`
}

// ErrInvalid: a session the store does not take.
var ErrInvalid = errors.New("invalid activity session")

// Limits of one session and one batch.
const (
	MaxBatch   = 500
	maxText    = 500
	maxExcerpt = 1000
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func valid(s Session) bool {
	if s.StartedAt.IsZero() || s.EndedAt.Before(s.StartedAt) || s.EndedAt.Sub(s.StartedAt) > 24*time.Hour {
		return false
	}
	for _, t := range []string{s.App, s.Bundle, s.Window, s.URL, s.Field} {
		if len([]rune(t)) > maxText {
			return false
		}
	}
	return len([]rune(s.Excerpt)) <= maxExcerpt
}

// Add stores a batch of the seat's sessions and deletes what is older than
// retention — the purge rides along with the writes, so a log nobody feeds
// any more stops growing and one that is fed never outgrows its window.
func (s *Store) Add(ctx context.Context, orgID, humanID uuid.UUID, batch []Session, retention time.Duration) (int, error) {
	if len(batch) == 0 || len(batch) > MaxBatch {
		return 0, ErrInvalid
	}
	for _, x := range batch {
		if !valid(x) {
			return 0, ErrInvalid
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // after Commit a no-op
	for _, x := range batch {
		if _, err := tx.Exec(ctx, `INSERT INTO human_activity
			(id, org_id, human_id, started_at, ended_at, app, bundle, window_title, url, field, excerpt)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			uuid.New(), orgID, humanID, x.StartedAt, x.EndedAt,
			strings.TrimSpace(x.App), strings.TrimSpace(x.Bundle), strings.TrimSpace(x.Window),
			strings.TrimSpace(x.URL), strings.TrimSpace(x.Field), strings.TrimSpace(x.Excerpt)); err != nil {
			return 0, err
		}
	}
	if retention > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM human_activity WHERE started_at < $1`, time.Now().Add(-retention)); err != nil {
			return 0, err
		}
	}
	return len(batch), tx.Commit(ctx)
}

// Between returns the seat's sessions that began in [from, to), in order.
func (s *Store) Between(ctx context.Context, humanID uuid.UUID, from, to time.Time) ([]Session, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, started_at, ended_at, app, bundle, window_title, url, field, excerpt
		FROM human_activity WHERE human_id=$1 AND started_at >= $2 AND started_at < $3 ORDER BY started_at`,
		humanID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var x Session
		if err := rows.Scan(&x.ID, &x.StartedAt, &x.EndedAt, &x.App, &x.Bundle, &x.Window, &x.URL, &x.Field, &x.Excerpt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Delete removes the seat's sessions that began in [from, to); a zero from
// and to removes all of them.
func (s *Store) Delete(ctx context.Context, humanID uuid.UUID, from, to time.Time) (int64, error) {
	var tag interface{ RowsAffected() int64 }
	var err error
	if from.IsZero() && to.IsZero() {
		tag, err = s.pool.Exec(ctx, `DELETE FROM human_activity WHERE human_id=$1`, humanID)
	} else {
		tag, err = s.pool.Exec(ctx, `DELETE FROM human_activity WHERE human_id=$1 AND started_at >= $2 AND started_at < $3`,
			humanID, from, to)
	}
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// Day is one day of the log with its number of sessions.
type Day struct {
	Day      string `json:"day"`
	Sessions int    `json:"sessions"`
	// Last is the end of the day's last session.
	Last time.Time `json:"-"`
}

// Days lists the seat's days with activity, newest first, as calendar days
// in loc.
func (s *Store) Days(ctx context.Context, humanID uuid.UUID, loc *time.Location) ([]Day, error) {
	rows, err := s.pool.Query(ctx, `SELECT started_at, ended_at FROM human_activity WHERE human_id=$1 ORDER BY started_at DESC`, humanID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Day{}
	for rows.Next() {
		var at, end time.Time
		if err := rows.Scan(&at, &end); err != nil {
			return nil, err
		}
		day := at.In(loc).Format("2006-01-02")
		if n := len(out); n > 0 && out[n-1].Day == day {
			out[n-1].Sessions++
			if end.After(out[n-1].Last) {
				out[n-1].Last = end
			}
		} else {
			out = append(out, Day{Day: day, Sessions: 1, Last: end})
		}
	}
	return out, rows.Err()
}
