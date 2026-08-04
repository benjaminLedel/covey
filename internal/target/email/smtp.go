package email

import (
	"crypto/tls"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SMTP side of the plugin: build and deliver messages. The stdlib is enough
// here — PLAIN auth over TLS/STARTTLS is the common denominator of all
// widespread providers; rebuilding protocols ourselves is off limits (guard
// rails).

// outgoing is a message to be delivered (send as well as reply).
type outgoing struct {
	From       string
	To         []string
	Cc         []string
	Subject    string
	Body       string
	InReplyTo  string
	References []string
}

// buildMessage serializes the message as RFC-5322 text: UTF-8 subject by
// Q-encoding, body quoted-printable, threading headers for replies. Recipient
// addresses are validated beforehand (parseAddrs) — header injection through
// smuggled-in line breaks is thereby ruled out.
func buildMessage(o outgoing, now time.Time) []byte {
	var b strings.Builder
	header := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	_, fromDomain, _ := strings.Cut(o.From, "@")
	header("From", o.From)
	header("To", strings.Join(o.To, ", "))
	header("Cc", strings.Join(o.Cc, ", "))
	header("Subject", mime.QEncoding.Encode("utf-8", sanitizeHeader(o.Subject)))
	header("Date", now.Format(time.RFC1123Z))
	header("Message-ID", fmt.Sprintf("<%s@%s>", uuid.NewString(), fromDomain))
	header("In-Reply-To", sanitizeHeader(o.InReplyTo))
	header("References", sanitizeHeader(strings.Join(o.References, " ")))
	header("MIME-Version", "1.0")
	header("Content-Type", `text/plain; charset=utf-8`)
	header("Content-Transfer-Encoding", "quoted-printable")
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&b)
	qp.Write([]byte(o.Body))
	qp.Close()
	return []byte(b.String())
}

// sendMail delivers the message to the SMTP server. Auth only if the server
// offers it (test doubles and internal relays get by without it).
func sendMail(cfg Config, o outgoing) error {
	msg := buildMessage(o, time.Now())
	rcpts := append(append([]string{}, o.To...), o.Cc...)

	var c *smtp.Client
	var err error
	if cfg.SMTPTLS == tlsImplicit {
		conn, dialErr := tls.Dial("tcp", cfg.SMTPAddr, tlsConfig(cfg.SMTPHost))
		if dialErr != nil {
			return fmt.Errorf("smtp connection %s: %w", cfg.SMTPAddr, dialErr)
		}
		c, err = smtp.NewClient(conn, cfg.SMTPHost)
	} else {
		c, err = smtp.Dial(cfg.SMTPAddr)
	}
	if err != nil {
		return fmt.Errorf("smtp connection %s: %w", cfg.SMTPAddr, err)
	}
	defer c.Close()

	if cfg.SMTPTLS == tlsStartTLS {
		if err := c.StartTLS(tlsConfig(cfg.SMTPHost)); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if ok, _ := c.Extension("AUTH"); ok {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.SMTPHost)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("smtp login as %s: %w", cfg.Username, err)
		}
	}
	if err := c.Mail(o.From); err != nil {
		return fmt.Errorf("smtp sender %s: %w", o.From, err)
	}
	for _, r := range rcpts {
		if err := c.Rcpt(r); err != nil {
			return fmt.Errorf("smtp recipient %s: %w", r, err)
		}
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

// parseAddrs validates and normalizes recipient addresses and enforces the send
// allowlist (COVEY_EMAIL_SEND_DOMAINS) — fail-closed per address.
func parseAddrs(field string, raw []string) ([]string, error) {
	out := []string{}
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		a, err := mail.ParseAddress(r)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid address %q: %w", field, r, err)
		}
		if !sendAllowed(a.Address) {
			return nil, fmt.Errorf("%s: %q lies outside the send allowlist (COVEY_EMAIL_SEND_DOMAINS)", field, a.Address)
		}
		out = append(out, a.Address)
	}
	return out, nil
}

// sanitizeHeader strips line breaks out of header values — the only place where
// user text (the subject) reaches a header.
func sanitizeHeader(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}

// tlsConfig sets the minimum version explicitly instead of relying on the Go
// default. That default is TLS 1.2 today and thereby right — but "right,
// because the language happens to prescribe it that way" is no ground to build
// an encryption on. Mailboxes are operated for years.
func tlsConfig(serverName string) *tls.Config {
	return &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}
}
