package httpapi

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/identity"
)

// The browser session (cookie → human) belongs to the HTTP layer itself: it is
// not an identity in the sense of internal/identity (which manages humans,
// roles and agent tokens), but the short-lived badge of ONE browser. That is
// why it stays here — but in one place instead of scattered across server.go
// and profile.go, so that knowledge of the schema does not live in four spots.
//
// Only the hash of the token is stored: whoever reads the database does not
// get usable sessions out of it.
type sessionStore struct{ pool *pgxpool.Pool }

func (s *Server) sessions() sessionStore { return sessionStore{pool: s.Pool} }

// Session is one entry of the session list in the profile.
type Session struct {
	TokenHash string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Create records a session.
func (st sessionStore) Create(ctx context.Context, tokenHash string, humanID uuid.UUID, expires time.Time) error {
	_, err := st.pool.Exec(ctx,
		`INSERT INTO http_sessions (token_hash, human_id, expires_at) VALUES ($1,$2,$3)`,
		tokenHash, humanID, expires)
	return err
}

// Principal resolves a session to the signed-in human. Expired sessions count
// as absent — the check sits inside the query so it cannot be forgotten.
func (st sessionStore) Principal(ctx context.Context, tokenHash string) (identity.Principal, error) {
	var p identity.Principal
	err := st.pool.QueryRow(ctx, `SELECT h.id, h.org_id, h.email, h.display_name, h.role
		FROM http_sessions s JOIN humans h ON h.id = s.human_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`, tokenHash).
		Scan(&p.ID, &p.OrgID, &p.Email, &p.DisplayName, &p.Role)
	return p, err
}

// Delete ends a single session (sign-out).
func (st sessionStore) Delete(ctx context.Context, tokenHash string) error {
	_, err := st.pool.Exec(ctx, "DELETE FROM http_sessions WHERE token_hash=$1", tokenHash)
	return err
}

// List returns a human's open sessions, newest first.
func (st sessionStore) List(ctx context.Context, humanID uuid.UUID) ([]Session, error) {
	rows, err := st.pool.Query(ctx, `SELECT token_hash, created_at, expires_at
		FROM http_sessions WHERE human_id=$1 AND expires_at > now() ORDER BY created_at DESC`, humanID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.TokenHash, &s.CreatedAt, &s.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteOthers signs out every other browser and leaves the current one alone.
func (st sessionStore) DeleteOthers(ctx context.Context, humanID uuid.UUID, keep string) (int64, error) {
	tag, err := st.pool.Exec(ctx,
		"DELETE FROM http_sessions WHERE human_id=$1 AND token_hash <> $2", humanID, keep)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
