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
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/secrets/sealbox"
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
	// SiteURL: the address under which this installation is reachable from
	// outside — the host every link in a mail is built from. Empty means the
	// environment (COVEY_SITE_URL) and, for the HTTP handlers, the request's
	// own origin; the mails sent from a loop have no request to ask and go out
	// without links (#180).
	//
	// A setting and not only a variable, because the person who notices that
	// the links are missing is the one reading the mail — and they should be
	// able to fix it where they fix the mail server, not in a redeploy.
	SiteURL = "site.url"
	// NotifyWindow: how long notification events are collected before a mail
	// goes out (a duration, "5m"). Zero sends on the next pass of the sender.
	NotifyWindow = "notify.window"
	// NotifyClassPrefix + class: the instance's master switch per notification
	// class, on | off. Off means nobody on this installation is written to
	// about the class, whatever their own account says.
	NotifyClassPrefix = "notify."
)

// The values of the notify.<class> switches.
const (
	On  = "on"
	Off = "off"
)

// NotifyClasses are the classes internal/notify knows, spelled out here so the
// settings table can carry a switch per class without importing the package
// that would import this one back.
var NotifyClasses = []string{"decision", "task", "cost", "ops"}

// NotifyClassKey names the master switch of one class.
func NotifyClassKey(class string) string { return NotifyClassPrefix + class }

// DefaultNotifyWindow is what applies while nobody has set notify.window.
const DefaultNotifyWindow = 5 * time.Minute

// MaxNotifyWindow bounds it: a day of collecting is a digest, and past that
// the "agent waits" the mails exist for has waited too long.
const MaxNotifyWindow = 24 * time.Hour

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
	SiteName:        "covey",
	SiteURL:         "",
	HomeStoreBackup: "",
	NotifyWindow:    "5m",
}

func init() {
	for _, c := range NotifyClasses {
		Defaults[NotifyClassKey(c)] = On
	}
}

var (
	ErrUnknownKey = errors.New("unknown setting")
	ErrInvalid    = errors.New("invalid value")
)

type Store struct {
	pool *pgxpool.Pool
	// box seals the secret settings (the SMTP password). It may be nil — a
	// caller without a master key can read and write every plain setting and
	// gets an error on the secret ones, which is better than a store that
	// silently keeps a password in clear text.
	box *sealbox.Box
}

func New(pool *pgxpool.Pool, box *sealbox.Box) *Store { return &Store{pool: pool, box: box} }

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
	if Secrets[key] {
		return fmt.Errorf("%w: %s is a secret — use SetSecret", ErrInvalid, key)
	}
	if ReadOnly[key] {
		return fmt.Errorf("%w: %s is written by the installation itself", ErrInvalid, key)
	}
	value = strings.TrimSpace(value)
	if err := validate(key, value); err != nil {
		return err
	}
	// The one check that needs the database and therefore cannot live in
	// validate: registration does not open while no mail has demonstrably
	// gone out. A filled-in SMTP host is not evidence, a delivered message is
	// — otherwise the first person to notice the typo is the one whose
	// verification link never arrives.
	if key == SignupMode && value != ModeOff {
		if err := s.mailerProven(ctx); err != nil {
			return err
		}
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
	case SiteURL:
		// Empty is allowed and means "not decided here". Anything else has to
		// be an absolute http(s) address: a bare host would produce links
		// without a scheme, and a path-only value links nowhere.
		if value == "" {
			return nil
		}
		u, err := url.Parse(value)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
			u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("%w: %s must be an absolute http(s) address such as https://covey.example.com", ErrInvalid, key)
		}
		return nil
	case NotifyWindow:
		d, err := time.ParseDuration(value)
		if err != nil || d < 0 || d > MaxNotifyWindow {
			return fmt.Errorf("%w: %s must be a duration between 0s and 24h, e.g. 5m", ErrInvalid, key)
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
	if strings.HasPrefix(key, "mail.") {
		return validateMail(key, value)
	}
	if strings.HasPrefix(key, NotifyClassPrefix) {
		if value != On && value != Off {
			return fmt.Errorf("%w: %s must be on or off", ErrInvalid, key)
		}
		return nil
	}
	return nil
}

// SiteURLValue answers the address links are built from: the setting, and
// the environment's value (handed in by the caller) where the setting is
// empty. Without a trailing slash, so a path can be appended directly. Empty
// when neither is set — the caller decides what that means for it.
func (s *Store) SiteURLValue(ctx context.Context, env string) string {
	v, err := s.Get(ctx, SiteURL)
	if err != nil || v == "" {
		v = env
	}
	return strings.TrimRight(strings.TrimSpace(v), "/")
}

// NotifyWindowValue answers the damping window. A broken database or an
// unparsable row answers the default rather than zero: "send at once" must
// never be what an error means.
func (s *Store) NotifyWindowValue(ctx context.Context) time.Duration {
	v, err := s.Get(ctx, NotifyWindow)
	if err != nil {
		return DefaultNotifyWindow
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return DefaultNotifyWindow
	}
	return d
}

// NotifyClassOn says whether the installation sends mails of one class at
// all. Fails OPEN on a database error, deliberately: the switch exists to
// quiet a class, and a mail that goes out although the store was unreachable
// is the lesser fault next to one that is silently dropped.
func (s *Store) NotifyClassOn(ctx context.Context, class string) bool {
	v, err := s.Get(ctx, NotifyClassKey(class))
	if err != nil {
		return true
	}
	return v != Off
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
