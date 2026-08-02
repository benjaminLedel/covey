package httpapi

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/identity"
)

// Die Browser-Session (Cookie → Mensch) gehört dem HTTP-Layer selbst: Sie ist
// keine Identität im Sinne von internal/identity (die verwaltet Menschen,
// Rollen und Agent-Tokens), sondern der kurzlebige Ausweis EINES Browsers.
// Deshalb bleibt sie hier — aber an einer Stelle statt verstreut über
// server.go und profile.go, damit die Kenntnis des Schemas nicht an vier Orten
// liegt.
//
// Gespeichert wird nur der Hash des Tokens: Wer die Datenbank liest, bekommt
// damit keine benutzbaren Sitzungen in die Hand.
type sessionStore struct{ pool *pgxpool.Pool }

func (s *Server) sessions() sessionStore { return sessionStore{pool: s.Pool} }

// Sitzung ist ein Eintrag der Sitzungsliste im Profil.
type Sitzung struct {
	TokenHash string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Create legt eine Sitzung an.
func (st sessionStore) Create(ctx context.Context, tokenHash string, humanID uuid.UUID, expires time.Time) error {
	_, err := st.pool.Exec(ctx,
		`INSERT INTO http_sessions (token_hash, human_id, expires_at) VALUES ($1,$2,$3)`,
		tokenHash, humanID, expires)
	return err
}

// Principal löst eine Sitzung zum angemeldeten Menschen auf. Abgelaufene
// Sitzungen gelten als nicht vorhanden — die Prüfung steckt in der Abfrage,
// damit sie nicht vergessen werden kann.
func (st sessionStore) Principal(ctx context.Context, tokenHash string) (identity.Principal, error) {
	var p identity.Principal
	err := st.pool.QueryRow(ctx, `SELECT h.id, h.org_id, h.email, h.display_name, h.role
		FROM http_sessions s JOIN humans h ON h.id = s.human_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`, tokenHash).
		Scan(&p.ID, &p.OrgID, &p.Email, &p.DisplayName, &p.Role)
	return p, err
}

// Delete beendet eine einzelne Sitzung (Abmelden).
func (st sessionStore) Delete(ctx context.Context, tokenHash string) error {
	_, err := st.pool.Exec(ctx, "DELETE FROM http_sessions WHERE token_hash=$1", tokenHash)
	return err
}

// List liefert die offenen Sitzungen eines Menschen, neueste zuerst.
func (st sessionStore) List(ctx context.Context, humanID uuid.UUID) ([]Sitzung, error) {
	rows, err := st.pool.Query(ctx, `SELECT token_hash, created_at, expires_at
		FROM http_sessions WHERE human_id=$1 AND expires_at > now() ORDER BY created_at DESC`, humanID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Sitzung
	for rows.Next() {
		var s Sitzung
		if err := rows.Scan(&s.TokenHash, &s.CreatedAt, &s.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteOthers meldet alle anderen Browser ab und lässt den aktuellen stehen.
func (st sessionStore) DeleteOthers(ctx context.Context, humanID uuid.UUID, behalten string) (int64, error) {
	tag, err := st.pool.Exec(ctx,
		"DELETE FROM http_sessions WHERE human_id=$1 AND token_hash <> $2", humanID, behalten)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
