package mail

// SMTP delivery. The stdlib is enough: PLAIN over TLS or STARTTLS is the
// common denominator of every widespread provider, and rebuilding protocols
// ourselves is off limits (design principles — crypto primitives yes, crypto
// protocols no).

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"time"

	"covey/internal/settings"
)

// ErrNotConfigured is what every send answers while no mail server is set.
// A distinct error rather than a generic failure, because the two callers
// react differently: the test mail says "configure a host first", registration
// says "registration is currently unavailable" and creates nothing.
var ErrNotConfigured = errors.New("no mail server configured")

// dialTimeout bounds the connection attempt. The pack's sender has none, and
// on a host that swallows packets that means an HTTP handler hanging until the
// kernel gives up — minutes, during which a person is looking at a spinner
// after pressing "send test mail".
const dialTimeout = 15 * time.Second

// SMTP is the built-in sender. It reads its configuration per send from the
// settings store rather than holding it: an administrator who corrects a typo
// in the host must not have to restart the instance for it.
type SMTP struct{ Settings *settings.Store }

func New(s *settings.Store) *SMTP { return &SMTP{Settings: s} }

func (s *SMTP) Configured(ctx context.Context) bool {
	cfg, err := s.Settings.Mail(ctx)
	return err == nil && cfg.Configured()
}

func (s *SMTP) Send(ctx context.Context, m Message) error {
	cfg, err := s.Settings.Mail(ctx)
	if err != nil {
		return err
	}
	if !cfg.Configured() {
		return ErrNotConfigured
	}
	return deliver(ctx, cfg, m)
}

func deliver(ctx context.Context, cfg settings.Mail, m Message) error {
	to, err := address("recipient", m.To)
	if err != nil {
		return err
	}
	from, err := address("mail.from", cfg.From)
	if err != nil {
		return err
	}
	msg := build(cfg.Sender(), from, Message{To: to, Subject: m.Subject, Body: m.Body}, time.Now())

	c, err := connect(ctx, cfg)
	if err != nil {
		return err
	}
	defer c.Close()

	// Auth only where the server offers it: a local double and an internal
	// relay get by without one, and demanding it would make them unusable.
	if ok, _ := c.Extension("AUTH"); ok && cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			return fmt.Errorf("smtp login as %s: %w", cfg.Username, err)
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("smtp sender %s: %w", from, err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("smtp recipient %s: %w", to, err)
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// connect opens the session in the security mode the configuration asks for.
func connect(ctx context.Context, cfg settings.Mail) (*smtp.Client, error) {
	d := &net.Dialer{Timeout: dialTimeout}
	addr := cfg.Addr()

	if cfg.Security == settings.SecurityTLS {
		conn, err := tls.DialWithDialer(d, "tcp", addr, tlsConfig(cfg.Host))
		if err != nil {
			return nil, fmt.Errorf("smtp connection %s: %w", addr, err)
		}
		return smtp.NewClient(conn, cfg.Host)
	}

	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("smtp connection %s: %w", addr, err)
	}
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("smtp connection %s: %w", addr, err)
	}
	if cfg.Security == settings.SecurityStartTLS {
		if err := c.StartTLS(tlsConfig(cfg.Host)); err != nil {
			c.Close()
			// The most common cause is a server that only speaks implicit TLS
			// on 465, so the error says which switch to reach for.
			return nil, fmt.Errorf("smtp starttls (try %s=%s on port 465): %w",
				settings.MailSecurity, settings.SecurityTLS, err)
		}
	}
	return c, nil
}

// tlsConfig sets the minimum version explicitly instead of relying on the Go
// default. That default is TLS 1.2 today and thereby right — but "right,
// because the language happens to prescribe it" is no ground to build an
// encryption on. Mailboxes are operated for years.
func tlsConfig(serverName string) *tls.Config {
	return &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}
}
