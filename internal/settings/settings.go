// Package settings holds the instance's own configuration — the switches an
// administrator operates, as opposed to the process configuration an operator
// sets before the binary can even reach the database (internal/config).
//
// The line runs there deliberately: COVEY_DATABASE_URL and COVEY_MASTER_KEY
// have to be known before this table can be read, so they stay in the
// environment. Everything an admin turns on and off during operation —
// whether this installation accepts registrations, how it sends mail — lives
// here, changeable through the product rather than through a redeploy
// (feature-requests/002-plattform-registrierung.md).
package settings

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Keys. Named as strings rather than as an enum because the table is a
// key/value store: a new switch is a constant plus a default, not a migration.
const (
	// SignupMode: off | waitlist | open — whether this installation accepts
	// self-registration at all.
	SignupMode = "signup.mode"
	// SignupOrgQuota: how many organisations one account may create.
	SignupOrgQuota = "signup.org_quota"
	// SiteName: what this installation calls itself on the sign-up page and
	// later in the mails it sends.
	SiteName = "site.name"
	// HomeStoreBackup: the date on which somebody confirmed that the block
	// store is in this installation's backup. Empty = nobody has yet.
	//
	// A setting that records a promise instead of enforcing one, and that is
	// the point: the platform cannot look into somebody's backup, but the
	// obligation it added (spec/16 — of a 7 GB home, 48 MB exist nowhere else)
	// has to be able to END. A check that stands on "!" forever is furniture
	// after two weeks, and then the finding next to it is not read either.
	HomeStoreBackup = "homestore.backup_confirmed"
)

// The modes of signup.mode.
const (
	ModeOff      = "off"
	ModeWaitlist = "waitlist"
	ModeOpen     = "open"
)

// Defaults are what applies while no row exists. That is the whole seeding
// mechanism: a fresh database needs none, and an installation that upgrades
// gets these values because the code says so — not because somebody remembered
// to leave an environment variable unset.
//
// signup.mode = off is the load-bearing one. An upgrade that silently opened a
// public registration form on somebody's internal instance would be an
// incident, not a feature.
var Defaults = map[string]string{
	SignupMode:      ModeOff,
	SignupOrgQuota:  "1",
	SiteName:        "Covey",
	HomeStoreBackup: "",
}

var (
	ErrUnknownKey = errors.New("unknown setting")
	ErrInvalid    = errors.New("invalid value")
)

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Get reads one setting, falling back to its default. An unknown key is an
// error rather than an empty string: whoever asks for a key that does not
// exist has a typo, and an empty answer would hide it.
func (s *Store) Get(ctx context.Context, key string) (string, error) {
	def, ok := Defaults[key]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownKey, key)
	}
	if s == nil || s.pool == nil {
		return def, nil
	}
	var value *string
	err := s.pool.QueryRow(ctx, `SELECT value FROM system_settings WHERE key=$1`, key).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && value == nil) {
		return def, nil
	}
	if err != nil {
		return def, err
	}
	return *value, nil
}

// All returns every setting with its effective value — stored or default.
// Sorted, so the CLI output and the API answer have a stable order.
func (s *Store) All(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string, len(Defaults))
	for k, v := range Defaults {
		out[k] = v
	}
	if s == nil || s.pool == nil {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT key, value FROM system_settings WHERE value IS NOT NULL`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var v string
		if err := rows.Scan(&k, &v); err != nil {
			return out, err
		}
		// Only keys the code knows: a leftover row from a removed feature must
		// not reappear as a setting nobody can explain.
		if _, ok := Defaults[k]; ok {
			out[k] = v
		}
	}
	return out, rows.Err()
}

// Keys lists the known settings in a stable order.
func Keys() []string {
	out := make([]string, 0, len(Defaults))
	for k := range Defaults {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Set stores a value. Validation happens HERE and not in the handler, so that
// the CLI and the API cannot disagree about what a valid value is.
//
// `by` is an ACCOUNT id since migration 0062 — the instance level does not
// require a seat, so a human id could not always be supplied.
func (s *Store) Set(ctx context.Context, key, value string, by *uuid.UUID) error {
	if _, ok := Defaults[key]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownKey, key)
	}
	value = strings.TrimSpace(value)
	if err := validate(key, value); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO system_settings (key, value, updated_by, updated_at)
		 VALUES ($1,$2,$3,now())
		 ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value,
		   updated_by=EXCLUDED.updated_by, updated_at=now()`, key, value, by)
	return err
}

func validate(key, value string) error {
	switch key {
	case SignupMode:
		switch value {
		case ModeOff, ModeWaitlist, ModeOpen:
			return nil
		}
		return fmt.Errorf("%w: %s must be one of off|waitlist|open", ErrInvalid, key)
	case SignupOrgQuota:
		var n int
		if _, err := fmt.Sscanf(value, "%d", &n); err != nil || n < 0 {
			return fmt.Errorf("%w: %s must be a number >= 0", ErrInvalid, key)
		}
		return nil
	case SiteName:
		if value == "" {
			return fmt.Errorf("%w: %s must not be empty", ErrInvalid, key)
		}
		return nil
	case HomeStoreBackup:
		// A date, or empty for "withdrawn". Deliberately not a boolean: what
		// makes the confirmation worth anything is WHEN it was given — a tick
		// from two years ago says something different from one from Tuesday.
		if value == "" {
			return nil
		}
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return fmt.Errorf("%w: %s must be an RFC3339 timestamp", ErrInvalid, key)
		}
		return nil
	}
	return nil
}

// Mode is the shorthand for the question the public website asks on every
// visit. A broken database answers "off": whether registration is open is a
// question that has to fail closed.
func (s *Store) Mode(ctx context.Context) string {
	v, err := s.Get(ctx, SignupMode)
	if err != nil {
		return ModeOff
	}
	return v
}
