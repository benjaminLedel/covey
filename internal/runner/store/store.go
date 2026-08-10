// Package store keeps the runners in Postgres: the row a runner authenticates
// against, and the built-in runner an organisation gets from the platform
// itself (spec/16-runner.md).
//
// Deliberately its own package next to internal/runner. That one holds the
// protocol and the runner side of it and must stay free of the database —
// on a remote host the runner is precisely the component that must not be a
// database client. internal/daemon carries the same cut for the same reason.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Kinds of runner. The built-in one runs inside the covey serve process and is
// created by the platform itself; a remote one has joined with a registration
// token.
const (
	KindBuiltin = "builtin"
	KindRemote  = "remote"
)

// ErrNotFound: no such runner — or not in this organisation.
var ErrNotFound = errors.New("runner not found")

// Runner is an execution node. It belongs to exactly one organisation: it holds
// homes and daemon tokens, and both are the property of one tenant (spec/16,
// "One runner, one organisation").
type Runner struct {
	ID         uuid.UUID  `json:"id"`
	OrgID      uuid.UUID  `json:"org_id"`
	Kind       string     `json:"kind"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// HashToken is the storage form of a runner token. Deliberately a plain SHA-256
// and not Argon2id: the token is 256 bits of entropy from a random source, so
// there is no dictionary to slow down — the cost of a KDF would buy nothing and
// would be paid on every allowlist request.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewToken generates a runner token.
func NewToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

const columns = `id, org_id, kind, name, created_at, last_seen_at`

func scan(row pgx.Row) (Runner, error) {
	var r Runner
	err := row.Scan(&r.ID, &r.OrgID, &r.Kind, &r.Name, &r.CreatedAt, &r.LastSeenAt)
	return r, err
}

// EnsureBuiltin returns an organisation's built-in runner and creates it if it
// is missing — for organisations that came into being after migration 0051.
func (s *Store) EnsureBuiltin(ctx context.Context, orgID uuid.UUID) (Runner, error) {
	r, err := scan(s.pool.QueryRow(ctx,
		`SELECT `+columns+` FROM runners WHERE org_id = $1 AND kind = 'builtin'`, orgID))
	if err == nil {
		return r, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Runner{}, err
	}
	// ON CONFLICT: two control planes on the same database may start at the
	// same time, and the partial unique index is what decides. Whoever loses
	// reads the winner's row instead of failing.
	_, err = s.pool.Exec(ctx,
		`INSERT INTO runners (id, org_id, kind) VALUES ($1, $2, 'builtin') ON CONFLICT DO NOTHING`,
		uuid.New(), orgID)
	if err != nil {
		return Runner{}, err
	}
	return scan(s.pool.QueryRow(ctx,
		`SELECT `+columns+` FROM runners WHERE org_id = $1 AND kind = 'builtin'`, orgID))
}

// SetTokenHash replaces a runner's token. The built-in runner rotates its token
// on every start of the control plane.
func (s *Store) SetTokenHash(ctx context.Context, id uuid.UUID, hash string) error {
	_, err := s.pool.Exec(ctx, `UPDATE runners SET token_hash = $2 WHERE id = $1`, id, hash)
	return err
}

// ByToken resolves a runner token to its runner. An empty hash never matches:
// that is the state of a runner whose token has not been set yet, and it must
// not be reachable with an empty Authorization header.
func (s *Store) ByToken(ctx context.Context, token string) (Runner, error) {
	if token == "" {
		return Runner{}, ErrNotFound
	}
	r, err := scan(s.pool.QueryRow(ctx,
		`SELECT `+columns+` FROM runners WHERE token_hash = $1 AND token_hash <> ''`, HashToken(token)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Runner{}, ErrNotFound
	}
	return r, err
}

// Seen records a sign of life. Failures are the caller's to ignore — a missing
// timestamp is a display flaw, not a reason to reject a request.
func (s *Store) Seen(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE runners SET last_seen_at = now() WHERE id = $1`, id)
	return err
}

// ListForOrg returns an organisation's runners, the built-in one first.
func (s *Store) ListForOrg(ctx context.Context, orgID uuid.UUID) ([]Runner, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+columns+` FROM runners WHERE org_id = $1
		 ORDER BY (kind = 'builtin') DESC, created_at`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Runner
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
