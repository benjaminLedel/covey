package settings

// The instance's mail configuration.
//
// It lives in the same table as the other switches, with one difference: the
// SMTP password is a SECRET. It is sealed with the master key and is
// write-only from the outside — the API answers whether a value is set, never
// the value, the same rule the SecretStore's previews already follow.
//
// Why here and not in internal/mail: the sender should not know a database.
// It asks for a Config and delivers; where that Config comes from is a
// question of configuration, and this package is the answer to it.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	// MailHost, MailPort: where the mail goes. Empty host = no mailer, and
	// that is the state a fresh installation is in.
	MailHost = "mail.smtp_host"
	MailPort = "mail.smtp_port"
	// MailUser is the login name. Often the sender address, but not always —
	// some providers want an account name instead.
	MailUser = "mail.smtp_user"
	// MailPassword is the only secret setting. Sealed, never returned.
	// #nosec G101 — the key the password is stored under, not the password.
	MailPassword = "mail.smtp_password"
	// MailFrom, MailFromName: envelope sender and the name in front of it.
	// An empty MailFromName falls back to SiteName.
	MailFrom     = "mail.from"
	MailFromName = "mail.from_name"
	// MailSecurity: starttls | tls | none.
	//
	// FR-002 wrote this as a boolean `mail.starttls`. It is an enum instead,
	// because implicit TLS on port 465 is not a rarity but the second half of
	// the installed base — a boolean would have made every such mail server
	// unreachable, and `none` has to exist so a local double (demo/fakemail on
	// 1025) can be used at all.
	MailSecurity = "mail.security"
	// MailLastTestAt, MailLastTestError: what the last test mail did. Written
	// by the installation, not by an administrator — which is why both are
	// read-only for Set.
	//
	// They are kept because whoever switches registration on a week later
	// should be able to SEE when this last worked, instead of having to
	// remember it.
	MailLastTestAt    = "mail.last_test_at"
	MailLastTestError = "mail.last_test_error"
)

// The security modes of MailSecurity.
const (
	SecurityStartTLS = "starttls" // plain connection, upgraded before login
	SecurityTLS      = "tls"      // TLS from the first byte (port 465)
	SecurityNone     = "none"     // plain text — a local double, never a real mailbox
)

// Secrets marks the keys whose value is sealed. They live in the same table,
// in nonce/ciphertext instead of value — which is why All() cannot leak them
// even by accident: it reads `value`, and for a secret that column is NULL.
var Secrets = map[string]bool{MailPassword: true}

// ReadOnly marks the keys the installation writes about itself. An
// administrator who could set mail.last_test_at by hand could open
// registration without a working mailer, and the gate below would be
// decoration.
var ReadOnly = map[string]bool{MailLastTestAt: true, MailLastTestError: true}

func init() {
	// Registered here rather than in the big Defaults literal so the mail
	// block stays in one file with the code that reads it.
	for k, v := range map[string]string{
		MailHost: "", MailPort: "587", MailUser: "", MailPassword: "",
		MailFrom: "", MailFromName: "", MailSecurity: SecurityStartTLS,
		MailLastTestAt: "", MailLastTestError: "",
	} {
		Defaults[k] = v
	}
}

func validateMail(key, value string) error {
	switch key {
	case MailPort:
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("%w: %s must be a port between 1 and 65535", ErrInvalid, key)
		}
	case MailFrom:
		if value == "" {
			return nil // clearing is allowed; sending then fails, loudly
		}
		if _, err := mail.ParseAddress(value); err != nil {
			return fmt.Errorf("%w: %s must be an e-mail address: %v", ErrInvalid, key, err)
		}
	case MailSecurity:
		switch value {
		case SecurityStartTLS, SecurityTLS, SecurityNone:
			return nil
		}
		return fmt.Errorf("%w: %s must be one of starttls|tls|none", ErrInvalid, key)
	case MailHost, MailUser, MailFromName:
		// A host with a scheme or a port glued on is the most frequent typo,
		// and it fails much later — in a DNS lookup nobody is watching.
		if key == MailHost && (strings.Contains(value, "/") || strings.Contains(value, ":")) {
			return fmt.Errorf("%w: %s is a host name, without a scheme and without a port (the port is %s)", ErrInvalid, key, MailPort)
		}
	}
	return nil
}

// --- Secret settings ---

// ErrNoMasterKey is what a store without a sealbox answers. It is a
// configuration error, not a bad request: whoever reaches here has a process
// without COVEY_MASTER_KEY, and no input would help.
var ErrNoMasterKey = errors.New("no master key: secret settings cannot be stored")

// aad binds the ciphertext to its key, so a sealed value cannot be moved to
// another setting's row.
func aad(key string) []byte { return []byte("system:" + key) }

// SetSecret stores a sealed value. An empty value clears it — that is how the
// UI's "remove password" works, and it keeps a separate delete endpoint out of
// the API.
func (s *Store) SetSecret(ctx context.Context, key, value string, by *uuid.UUID) error {
	if !Secrets[key] {
		return fmt.Errorf("%w: %s is not a secret setting", ErrUnknownKey, key)
	}
	if value == "" {
		_, err := s.pool.Exec(ctx,
			`INSERT INTO system_settings (key, value, nonce, ciphertext, updated_by, updated_at)
			 VALUES ($1,NULL,NULL,NULL,$2,now())
			 ON CONFLICT (key) DO UPDATE SET value=NULL, nonce=NULL, ciphertext=NULL,
			   updated_by=EXCLUDED.updated_by, updated_at=now()`, key, by)
		return err
	}
	if s.box == nil {
		return ErrNoMasterKey
	}
	nonce, ct, err := s.box.Seal(aad(key), value)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO system_settings (key, value, nonce, ciphertext, updated_by, updated_at)
		 VALUES ($1,NULL,$2,$3,$4,now())
		 ON CONFLICT (key) DO UPDATE SET value=NULL, nonce=EXCLUDED.nonce,
		   ciphertext=EXCLUDED.ciphertext, updated_by=EXCLUDED.updated_by, updated_at=now()`,
		key, nonce, ct, by)
	return err
}

// GetSecret opens a sealed value. Only the code calls this — the API never
// does.
func (s *Store) GetSecret(ctx context.Context, key string) (string, error) {
	if !Secrets[key] {
		return "", fmt.Errorf("%w: %s is not a secret setting", ErrUnknownKey, key)
	}
	if s == nil || s.pool == nil {
		return "", nil
	}
	var nonce, ct []byte
	err := s.pool.QueryRow(ctx,
		`SELECT nonce, ciphertext FROM system_settings WHERE key=$1`, key).Scan(&nonce, &ct)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && len(ct) == 0) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if s.box == nil {
		return "", ErrNoMasterKey
	}
	return s.box.Open("setting "+key, aad(key), nonce, ct)
}

// SecretSet answers the only question the outside gets to ask about a secret:
// is one there?
func (s *Store) SecretSet(ctx context.Context, key string) (bool, error) {
	if s == nil || s.pool == nil {
		return false, nil
	}
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM system_settings WHERE key=$1 AND ciphertext IS NOT NULL`, key).Scan(&n)
	return n > 0, err
}

// --- The mailer's configuration ---

// Mail is what a sender needs. A struct rather than nine Get calls, because
// the parts only make sense together: a host without a from-address delivers
// nothing.
type Mail struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	FromName string
	Security string
}

// Configured says whether a mailer exists at all. Host and sender are the two
// without which nothing can be attempted.
func (m Mail) Configured() bool { return m.Host != "" && m.From != "" }

// Addr is the dial address.
func (m Mail) Addr() string { return net.JoinHostPort(m.Host, strconv.Itoa(m.Port)) }

// Sender renders the From header — with a display name when one is set.
func (m Mail) Sender() string {
	if m.FromName == "" {
		return m.From
	}
	return (&mail.Address{Name: m.FromName, Address: m.From}).String()
}

// Mail reads the whole mail configuration, secret included. It is the one
// place that opens the password, and it is called per send — an administrator
// who corrects a typo in the host must not have to restart the instance.
func (s *Store) Mail(ctx context.Context) (Mail, error) {
	all, err := s.All(ctx)
	if err != nil {
		return Mail{}, err
	}
	port, _ := strconv.Atoi(all[MailPort])
	if port == 0 {
		port = 587
	}
	m := Mail{
		Host: all[MailHost], Port: port, Username: all[MailUser],
		From: all[MailFrom], FromName: all[MailFromName], Security: all[MailSecurity],
	}
	if m.FromName == "" {
		m.FromName = all[SiteName]
	}
	pw, err := s.GetSecret(ctx, MailPassword)
	if err != nil {
		return m, err
	}
	m.Password = pw
	return m, nil
}

// RecordMailTest writes down what the last test did. `sendErr` empty means it
// worked.
func (s *Store) RecordMailTest(ctx context.Context, at time.Time, sendErr string) error {
	for key, value := range map[string]string{
		MailLastTestAt:    at.UTC().Format(time.RFC3339),
		MailLastTestError: sendErr,
	} {
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO system_settings (key, value, updated_at) VALUES ($1,$2,now())
			 ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()`,
			key, value); err != nil {
			return err
		}
	}
	return nil
}

// mailerProven is the gate in front of signup.mode. It reads the record of the
// last test, not the configuration: a host that is filled in proves nothing.
func (s *Store) mailerProven(ctx context.Context) error {
	all, err := s.All(ctx)
	if err != nil {
		return err
	}
	if all[MailLastTestAt] == "" {
		return fmt.Errorf("%w: registration stays closed while no test mail has gone out — configure the mail server and send a test mail first", ErrInvalid)
	}
	if all[MailLastTestError] != "" {
		return fmt.Errorf("%w: the last test mail failed (%s) — registration stays closed until one gets through", ErrInvalid, all[MailLastTestError])
	}
	return nil
}
