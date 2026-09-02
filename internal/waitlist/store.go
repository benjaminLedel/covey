package waitlist

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The reasons a code does not work. They are told apart deliberately: "already
// used up" and "expired" send whoever holds the code down different paths, and
// a single "invalid" would leave them guessing. Nothing is disclosed by it —
// only the holder of a code learns anything at all, and only about their own.
var (
	ErrUnknown       = errors.New("this code is unknown")
	ErrRevoked       = errors.New("this code has been revoked")
	ErrExpired       = errors.New("this code has expired")
	ErrUsedUp        = errors.New("this code has already been used up")
	ErrEmailMismatch = errors.New("this code applies to a different e-mail address")
	ErrMalformed     = errors.New("this is not a covey code")
)

// Code is a waitlist code as the administration sees it — without its
// plaintext, which exists only at creation.
type Code struct {
	Hash         string     `json:"hash"`
	Label        string     `json:"label"`
	MaxUses      int        `json:"max_uses"`
	UsedCount    int        `json:"used_count"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	OrgID        *uuid.UUID `json:"org_id,omitempty"`
	EmailPattern string     `json:"email_pattern,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
}

// Open reports whether this code could still be redeemed right now.
func (c Code) Open() bool {
	return c.RevokedAt == nil && c.UsedCount < c.MaxUses &&
		(c.ExpiresAt == nil || c.ExpiresAt.After(time.Now()))
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Options are the conditions attached to a new code.
type Options struct {
	Label        string
	MaxUses      int
	ExpiresAt    *time.Time
	OrgID        *uuid.UUID
	EmailPattern string
	// CreatedBy is an ACCOUNT id since migration 0062: whoever hands out codes
	// administers the installation and need not sit in one of its
	// organisations.
	CreatedBy *uuid.UUID
}

// Create draws a code, stores its hash and returns the plaintext — once.
func (s *Store) Create(ctx context.Context, opt Options) (string, error) {
	code, err := NewCode()
	if err != nil {
		return "", err
	}
	canonical, ok := Normalize(code)
	if !ok {
		return "", ErrMalformed // cannot happen; a broken generator must not go unnoticed
	}
	if opt.MaxUses <= 0 {
		opt.MaxUses = 1
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO waitlist_codes (code_hash, label, max_uses, expires_at, org_id, email_pattern, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		Hash(canonical), strings.TrimSpace(opt.Label), opt.MaxUses, opt.ExpiresAt,
		opt.OrgID, strings.ToLower(strings.TrimSpace(opt.EmailPattern)), opt.CreatedBy)
	if err != nil {
		return "", err
	}
	return code, nil
}

// List returns the codes, newest first — without plaintext, because there is
// none.
func (s *Store) List(ctx context.Context) ([]Code, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT code_hash, label, max_uses, used_count, expires_at, org_id, email_pattern, created_at, revoked_at
		 FROM waitlist_codes ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Code
	for rows.Next() {
		var c Code
		if err := rows.Scan(&c.Hash, &c.Label, &c.MaxUses, &c.UsedCount, &c.ExpiresAt,
			&c.OrgID, &c.EmailPattern, &c.CreatedAt, &c.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Revoke stops a code without deleting it — who redeemed it stays visible.
func (s *Store) Revoke(ctx context.Context, hashPrefix string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE waitlist_codes SET revoked_at=now() WHERE code_hash LIKE $1 || '%' AND revoked_at IS NULL`,
		hashPrefix)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUnknown
	}
	return nil
}

// Redeem books one use of the code inside the caller's transaction — the same
// one in which the account comes into being. That is the whole point: two
// sign-ups arriving at the same second with a one-use code must not both
// succeed, and an account whose code was booked but which then fails to be
// created must not exist either. `FOR UPDATE` locks the row for the duration
// of the transaction, so the second request waits and then finds the code used
// up.
func (s *Store) Redeem(ctx context.Context, tx pgx.Tx, code, email string, accountID uuid.UUID) (Code, error) {
	canonical, ok := Normalize(code)
	if !ok {
		return Code{}, ErrMalformed
	}
	hash := Hash(canonical)

	var c Code
	err := tx.QueryRow(ctx,
		`SELECT code_hash, label, max_uses, used_count, expires_at, org_id, email_pattern, created_at, revoked_at
		 FROM waitlist_codes WHERE code_hash=$1 FOR UPDATE`, hash).
		Scan(&c.Hash, &c.Label, &c.MaxUses, &c.UsedCount, &c.ExpiresAt,
			&c.OrgID, &c.EmailPattern, &c.CreatedAt, &c.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Code{}, ErrUnknown
	}
	if err != nil {
		return Code{}, err
	}
	switch {
	case c.RevokedAt != nil:
		return Code{}, ErrRevoked
	case c.ExpiresAt != nil && !c.ExpiresAt.After(time.Now()):
		return Code{}, ErrExpired
	case c.UsedCount >= c.MaxUses:
		return Code{}, ErrUsedUp
	}
	if c.EmailPattern != "" && !matchesPattern(email, c.EmailPattern) {
		return Code{}, ErrEmailMismatch
	}

	if _, err := tx.Exec(ctx,
		`UPDATE waitlist_codes SET used_count = used_count + 1 WHERE code_hash=$1`, hash); err != nil {
		return Code{}, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO waitlist_redemptions (code_hash, account_id) VALUES ($1,$2)`, hash, accountID); err != nil {
		return Code{}, err
	}
	c.UsedCount++
	return c, nil
}

// matchesPattern compares an address against the code's restriction: either a
// domain ("@firma.de") or a full address.
//
// A domain matches exactly, NOT its subdomains: erika@sub.firma.de does not
// get in on "@firma.de". This is a gate, and a gate should be narrow — whoever
// needs the subdomain names it. The "@" in the pattern is what anchors it;
// without it "@firma.de" would also let "boesefirma.de" through.
func matchesPattern(email, pattern string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if strings.HasPrefix(pattern, "@") {
		return strings.HasSuffix(email, pattern)
	}
	return email == pattern
}
