package accounts

// The one-time links: confirming an address, and getting back into an account
// whose password is gone (#168).
//
// Both are the same mechanism and differ only in what redeeming them does. The
// token itself is 32 random bytes, URL-safe; stored is only its SHA-256, as
// with sessions and waitlist codes. The clear text exists exactly once, in the
// mail — and that is what the whole construction rests on: whoever can read
// the mail owns the address.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Purposes.
const (
	PurposeVerify = "verify"
	PurposeReset  = "reset"
)

// Lifetimes. Different because the risks differ: a confirmation link may lie
// in a mailbox for a day without anything being at stake, while a reset link
// is a way into an existing account and belongs to the hour somebody asked
// for it.
const (
	VerifyTTL = 24 * time.Hour
	ResetTTL  = time.Hour
)

// ErrBadToken is the answer to every unusable token — unknown, expired,
// already used. Deliberately one error and not three: the three cases are
// distinguishable to whoever holds a valid token and to nobody else, and an
// answer that told them apart would let somebody probe.
var ErrBadToken = errors.New("this link is not valid (any more)")

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// execer is what IssueTokenIn needs — the pool and a transaction both satisfy
// it. The transaction is not a nicety: the confirmation token is issued in the
// same transaction that creates the account and redeems the waitlist code, so
// that a failed mail leaves none of the three behind.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// IssueToken draws a token for an account and stores its hash. The returned
// string is the only copy.
func (s *Store) IssueToken(ctx context.Context, accountID uuid.UUID, purpose string, ttl time.Duration) (string, error) {
	return IssueTokenIn(ctx, s.pool, accountID, purpose, ttl)
}

// IssueTokenIn is IssueToken against a given executor — a transaction, as a
// rule.
func IssueTokenIn(ctx context.Context, q execer, accountID uuid.UUID, purpose string, ttl time.Duration) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	_, err := q.Exec(ctx,
		`INSERT INTO account_tokens (token_hash, account_id, purpose, expires_at)
		 VALUES ($1,$2,$3,$4)`,
		hashToken(token), accountID, purpose, time.Now().Add(ttl))
	if err != nil {
		return "", err
	}
	return token, nil
}

// redeem marks a token as used and returns whose it was. The UPDATE carries
// the conditions instead of a preceding SELECT: two clicks arriving at the
// same moment must not both succeed, and only the database can decide that.
func (s *Store) redeem(ctx context.Context, tx pgx.Tx, token, purpose string) (uuid.UUID, error) {
	var accountID uuid.UUID
	err := tx.QueryRow(ctx,
		`UPDATE account_tokens SET used_at=now()
		 WHERE token_hash=$1 AND purpose=$2 AND used_at IS NULL AND expires_at > now()
		 RETURNING account_id`, hashToken(token), purpose).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrBadToken
	}
	return accountID, err
}

// Verify redeems a confirmation link: the token is used up and the address
// counts as confirmed. Both in one transaction — a token consumed without the
// confirmation being written would lock the account out of ever being
// confirmed.
//
// An already confirmed account is confirmed again without complaint. Whoever
// clicks the link twice has done nothing wrong.
func (s *Store) Verify(ctx context.Context, token string) (Account, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback(ctx)

	accountID, err := s.redeem(ctx, tx, token, PurposeVerify)
	if err != nil {
		return Account{}, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE accounts SET email_verified_at=COALESCE(email_verified_at, now()) WHERE id=$1`,
		accountID); err != nil {
		return Account{}, err
	}
	acc, err := byID(ctx, tx, accountID)
	if err != nil {
		return Account{}, err
	}
	return acc, tx.Commit(ctx)
}

// ResetPassword redeems a reset link and sets the new password.
//
// It confirms the address as a side effect, and that is not a shortcut: the
// mail with this link went to that address, and it was read. Somebody who
// registered with a typo and never confirmed still cannot get here — the mail
// would have gone to the typo.
//
// Every other reset token of this account dies with it. A second, older link
// lying in the same mailbox must not still be able to change the password
// after the first one has.
func (s *Store) ResetPassword(ctx context.Context, token, passwordHash string) (Account, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback(ctx)

	accountID, err := s.redeem(ctx, tx, token, PurposeReset)
	if err != nil {
		return Account{}, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE accounts SET password_hash=$2, email_verified_at=COALESCE(email_verified_at, now())
		 WHERE id=$1`, accountID, passwordHash); err != nil {
		return Account{}, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE account_tokens SET used_at=now()
		 WHERE account_id=$1 AND purpose=$2 AND used_at IS NULL`,
		accountID, PurposeReset); err != nil {
		return Account{}, err
	}
	acc, err := byID(ctx, tx, accountID)
	if err != nil {
		return Account{}, err
	}
	return acc, tx.Commit(ctx)
}

// DropTokens removes the unused tokens of one purpose. Used when a new link is
// issued for something that must exist only once at a time.
func (s *Store) DropTokens(ctx context.Context, accountID uuid.UUID, purpose string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM account_tokens WHERE account_id=$1 AND purpose=$2 AND used_at IS NULL`,
		accountID, purpose)
	return err
}

// PurgeExpiredTokens clears out what nobody can use any more. Not urgent and
// therefore not a job of its own: whoever issues a token sweeps up behind
// themselves.
func (s *Store) PurgeExpiredTokens(ctx context.Context) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM account_tokens WHERE expires_at < now() - interval '7 days'`)
	return err
}

// queryRower is what byID needs — a pool and a transaction both satisfy it.
type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func byID(ctx context.Context, q queryRower, id uuid.UUID) (Account, error) {
	var a Account
	err := q.QueryRow(ctx,
		`SELECT id, email, display_name, email_verified_at, platform_role, created_at
		 FROM accounts WHERE id=$1`, id).
		Scan(&a.ID, &a.Email, &a.DisplayName, &a.VerifiedAt, &a.PlatformRole, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	return a, err
}

// ByID reads one account.
func (s *Store) ByID(ctx context.Context, id uuid.UUID) (Account, error) {
	return byID(ctx, s.pool, id)
}
