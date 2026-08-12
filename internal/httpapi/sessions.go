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

// Create records a session. humanID is the ACTIVE membership and may be the
// zero UUID: an account without an organisation is signed in, it just has no
// seat to work from yet.
func (st sessionStore) Create(ctx context.Context, tokenHash string, accountID, humanID uuid.UUID, expires time.Time) error {
	var seat *uuid.UUID
	if humanID != uuid.Nil {
		seat = &humanID
	}
	_, err := st.pool.Exec(ctx,
		`INSERT INTO http_sessions (token_hash, account_id, human_id, expires_at) VALUES ($1,$2,$3,$4)`,
		tokenHash, accountID, seat, expires)
	return err
}

// Principal resolves a session to the signed-in account plus the seat it works
// from. Expired sessions count as absent — the check sits inside the query so
// it cannot be forgotten.
//
// The join to humans is a LEFT JOIN: a session without a seat is valid, and it
// then carries the account with an empty organisation. An INNER JOIN would
// make such a session look like no session at all — and whoever just
// registered would be thrown back to the login form.
func (st sessionStore) Principal(ctx context.Context, tokenHash string) (identity.Principal, error) {
	var p identity.Principal
	var humanID, orgID *uuid.UUID
	var role *string
	err := st.pool.QueryRow(ctx, `SELECT a.id, a.email, a.display_name, a.platform_role,
			h.id, h.org_id, h.role
		FROM http_sessions s
		JOIN accounts a ON a.id = s.account_id
		LEFT JOIN humans h ON h.id = s.human_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`, tokenHash).
		Scan(&p.AccountID, &p.Email, &p.DisplayName, &p.PlatformRole, &humanID, &orgID, &role)
	if err != nil {
		return identity.Principal{}, err
	}
	if humanID != nil {
		p.ID, p.OrgID, p.Role = *humanID, *orgID, *role
	}
	return p, nil
}

// Delete ends a single session (sign-out).
func (st sessionStore) Delete(ctx context.Context, tokenHash string) error {
	_, err := st.pool.Exec(ctx, "DELETE FROM http_sessions WHERE token_hash=$1", tokenHash)
	return err
}

// List returns an account's open sessions, newest first. Deliberately per
// ACCOUNT and not per seat: the list answers "where am I signed in?", and that
// is a question about the person, not about one of their organisations.
func (st sessionStore) List(ctx context.Context, accountID uuid.UUID) ([]Session, error) {
	rows, err := st.pool.Query(ctx, `SELECT token_hash, created_at, expires_at
		FROM http_sessions WHERE account_id=$1 AND expires_at > now() ORDER BY created_at DESC`, accountID)
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
func (st sessionStore) DeleteOthers(ctx context.Context, accountID uuid.UUID, keep string) (int64, error) {
	tag, err := st.pool.Exec(ctx,
		"DELETE FROM http_sessions WHERE account_id=$1 AND token_hash <> $2", accountID, keep)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
