// Package accounts holds the login identity — the person above the
// membership.
//
// Until now a login WAS a membership: one row in humans carried e-mail,
// password, organisation and role at once. That rules out one person working
// in two organisations, and it rules out a person existing before their
// organisation does — which is exactly what self-registration needs: first the
// account, then joining or founding (feature-requests/002-plattform-registrierung.md).
//
// humans therefore stays what it is, the seat in an organisation that ten
// foreign keys point at. This package is the layer above it.
package accounts

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound   = errors.New("account not found")
	ErrEmailTaken = errors.New("this e-mail address is already registered")
)

// Platform roles — the instance level, deliberately not an org role.
const (
	RoleUser        = "user"
	RoleSystemAdmin = "system_admin"
)

type Account struct {
	ID           uuid.UUID  `json:"id"`
	Email        string     `json:"email"`
	DisplayName  string     `json:"display_name"`
	VerifiedAt   *time.Time `json:"email_verified_at,omitempty"`
	PlatformRole string     `json:"platform_role"`
	CreatedAt    time.Time  `json:"created_at"`
}

// Verified reports whether the address has been confirmed.
func (a Account) Verified() bool { return a.VerifiedAt != nil }

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Registration is what a sign-up brings along.
type Registration struct {
	Email        string
	DisplayName  string
	PasswordHash string
	// Verified marks the address as confirmed straight away. True as long as
	// no mailer is configured: a confirmation nobody can send would be an
	// account nobody ever uses.
	Verified bool
}

// Register creates an account and runs `withinTx` in the SAME transaction —
// that is where the waitlist code is redeemed.
//
// The two belong together or not at all: a code booked for an account that was
// never created is a use burned for nothing, and an account created from a
// code that was already used up is a gate that did not hold. Whoever redeems
// separately has to reconcile the two cases by hand afterwards, and will get
// it wrong on the day two sign-ups arrive in the same second.
func (s *Store) Register(ctx context.Context, reg Registration,
	withinTx func(pgx.Tx, uuid.UUID) error) (Account, error) {

	email := strings.ToLower(strings.TrimSpace(reg.Email))
	if email == "" || !strings.Contains(email, "@") {
		return Account{}, errors.New("e-mail address is required")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback(ctx)

	// Eine Adresse, die schon einen SITZ hat, darf sich nicht selbst
	// registrieren. Sonst wählte ein Fremder das Passwort zu einer bestehenden
	// Mitgliedschaft: bis das Konto mit dem Sitz verknüpft ist (P1), fiele das
	// nicht auf — danach hinge sein Passwort an deren Organisation.
	//
	// Die Prüfung sitzt hier und nicht im Handler, weil sie in dieselbe
	// Transaktion gehört wie das Anlegen: zwischen Prüfen und Einfügen darf
	// kein anderer Vorgang liegen.
	var seat bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM humans WHERE lower(email)=$1)`, email).Scan(&seat); err != nil {
		return Account{}, err
	}
	if seat {
		return Account{}, ErrEmailTaken
	}

	a := Account{ID: uuid.New(), Email: email,
		DisplayName: strings.TrimSpace(reg.DisplayName), PlatformRole: RoleUser}
	var verifiedAt *time.Time
	if reg.Verified {
		now := time.Now()
		verifiedAt = &now
	}
	err = tx.QueryRow(ctx,
		`INSERT INTO accounts (id, email, password_hash, display_name, email_verified_at)
		 VALUES ($1,$2,$3,$4,$5) RETURNING created_at, email_verified_at`,
		a.ID, a.Email, reg.PasswordHash, a.DisplayName, verifiedAt).
		Scan(&a.CreatedAt, &a.VerifiedAt)
	if isUniqueViolation(err) {
		return Account{}, ErrEmailTaken
	}
	if err != nil {
		return Account{}, err
	}

	if withinTx != nil {
		if err := withinTx(tx, a.ID); err != nil {
			return Account{}, err
		}
	}
	return a, tx.Commit(ctx)
}

// ByEmail reads an account by its address.
func (s *Store) ByEmail(ctx context.Context, email string) (Account, error) {
	var a Account
	err := s.pool.QueryRow(ctx,
		`SELECT id, email, display_name, email_verified_at, platform_role, created_at
		 FROM accounts WHERE email=$1`, strings.ToLower(strings.TrimSpace(email))).
		Scan(&a.ID, &a.Email, &a.DisplayName, &a.VerifiedAt, &a.PlatformRole, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	return a, err
}

// SetPlatformRole raises or lowers the instance level. Deliberately without an
// HTTP route: system_admin is not something an organisation may hand to
// itself.
func (s *Store) SetPlatformRole(ctx context.Context, email, role string) error {
	if role != RoleUser && role != RoleSystemAdmin {
		return errors.New("role must be user or system_admin")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE accounts SET platform_role=$2 WHERE email=$1`,
		strings.ToLower(strings.TrimSpace(email)), role)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
