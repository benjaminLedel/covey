package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrInvalidPattern meldet ein syntaktisch unbrauchbares Allowlist-Muster.
var ErrInvalidPattern = errors.New("ungültiges egress-muster")

// Store hält die von der Oberfläche verwaltete Egress-Allowlist in Postgres.
// Sie ist plattform-global: der Egress-Proxy ist ein einziger Prozess und kann
// eingehende Sandbox-Verbindungen nicht pro Organisation unterscheiden. Wer
// darf sie ändern, entscheidet die RBAC-Schicht (security/platform_admin).
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Entry ist ein von einem Menschen gepflegter Allowlist-Eintrag.
type Entry struct {
	ID        uuid.UUID `json:"id"`
	Pattern   string    `json:"pattern"`
	Note      string    `json:"note"`
	CreatedAt string    `json:"created_at"`
}

// List liefert alle Einträge, alphabetisch nach Muster.
func (s *Store) List(ctx context.Context) ([]Entry, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, pattern, note, created_at FROM egress_allow ORDER BY pattern`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var createdAt time.Time
		if err := rows.Scan(&e.ID, &e.Pattern, &e.Note, &createdAt); err != nil {
			return nil, err
		}
		e.CreatedAt = createdAt.Format(time.RFC3339)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Patterns liefert nur die Muster — für den Allowlist-Aufbau des Proxy.
func (s *Store) Patterns(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT pattern FROM egress_allow`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Add legt ein Muster an (idempotent: doppelte Muster werden zusammengeführt).
func (s *Store) Add(ctx context.Context, pattern, note string) (Entry, error) {
	norm, err := NormalizePattern(pattern)
	if err != nil {
		return Entry{}, err
	}
	var e Entry
	var createdAt time.Time
	err = s.pool.QueryRow(ctx,
		`INSERT INTO egress_allow (pattern, note) VALUES ($1,$2)
		 ON CONFLICT (pattern) DO UPDATE SET note = EXCLUDED.note
		 RETURNING id, pattern, note, created_at`,
		norm, strings.TrimSpace(note)).Scan(&e.ID, &e.Pattern, &e.Note, &createdAt)
	if err != nil {
		return Entry{}, err
	}
	e.CreatedAt = createdAt.Format(time.RFC3339)
	return e, nil
}

// Delete entfernt einen Eintrag.
func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM egress_allow WHERE id=$1`, id)
	return err
}

// NormalizePattern validiert und normalisiert ein Host-Muster:
// klein geschrieben, getrimmt, ohne Schema/Pfad, optional "*."-Präfix.
func NormalizePattern(raw string) (string, error) {
	p := strings.ToLower(strings.TrimSpace(raw))
	if p == "" {
		return "", fmt.Errorf("%w: leer", ErrInvalidPattern)
	}
	if strings.ContainsAny(p, " \t/") || strings.Contains(p, "://") {
		return "", fmt.Errorf("%w: nur Host (ohne Schema/Pfad/Leerzeichen)", ErrInvalidPattern)
	}
	// Port abtrennen, falls angegeben — die Allowlist matcht auf dem Host.
	host := p
	if strings.HasPrefix(host, "*.") {
		host = host[2:]
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host == "" || !strings.Contains(host, ".") {
		return "", fmt.Errorf("%w: kein gültiger Host", ErrInvalidPattern)
	}
	return p, nil
}
