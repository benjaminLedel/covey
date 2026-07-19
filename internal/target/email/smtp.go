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

// SMTP-Seite des Plugins: Nachrichten bauen und versenden. Die Stdlib reicht
// hier — PLAIN-Auth über TLS/STARTTLS ist der gemeinsame Nenner aller
// gängigen Provider; eigene Protokoll-Nachbauten sind tabu (Leitplanken).

// outgoing ist eine zu versendende Nachricht (send wie reply).
type outgoing struct {
	From       string
	To         []string
	Cc         []string
	Subject    string
	Body       string
	InReplyTo  string
	References []string
}

// buildMessage serialisiert die Nachricht als RFC-5322-Text: UTF-8-Betreff
// per Q-Encoding, Body quoted-printable, Threading-Header für Antworten.
// Empfänger-Adressen sind vorab validiert (parseAddrs) — Header-Injection
// über eingeschleuste Zeilenumbrüche ist damit ausgeschlossen.
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

// sendMail liefert die Nachricht beim SMTP-Server ein. Auth nur, wenn der
// Server sie anbietet (Test-Doubles und interne Relays kommen ohne aus).
func sendMail(cfg Config, o outgoing) error {
	msg := buildMessage(o, time.Now())
	rcpts := append(append([]string{}, o.To...), o.Cc...)

	var c *smtp.Client
	var err error
	if cfg.SMTPTLS == tlsImplicit {
		conn, dialErr := tls.Dial("tcp", cfg.SMTPAddr, &tls.Config{ServerName: cfg.SMTPHost})
		if dialErr != nil {
			return fmt.Errorf("smtp-verbindung %s: %w", cfg.SMTPAddr, dialErr)
		}
		c, err = smtp.NewClient(conn, cfg.SMTPHost)
	} else {
		c, err = smtp.Dial(cfg.SMTPAddr)
	}
	if err != nil {
		return fmt.Errorf("smtp-verbindung %s: %w", cfg.SMTPAddr, err)
	}
	defer c.Close()

	if cfg.SMTPTLS == tlsStartTLS {
		if err := c.StartTLS(&tls.Config{ServerName: cfg.SMTPHost}); err != nil {
			return fmt.Errorf("smtp-starttls: %w", err)
		}
	}
	if ok, _ := c.Extension("AUTH"); ok {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.SMTPHost)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("smtp-login als %s: %w", cfg.Username, err)
		}
	}
	if err := c.Mail(o.From); err != nil {
		return fmt.Errorf("smtp-absender %s: %w", o.From, err)
	}
	for _, r := range rcpts {
		if err := c.Rcpt(r); err != nil {
			return fmt.Errorf("smtp-empfänger %s: %w", r, err)
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

// parseAddrs validiert und normalisiert Empfänger-Adressen und erzwingt die
// Versand-Allowlist (COVEY_EMAIL_SEND_DOMAINS) — fail-closed pro Adresse.
func parseAddrs(field string, raw []string) ([]string, error) {
	out := []string{}
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		a, err := mail.ParseAddress(r)
		if err != nil {
			return nil, fmt.Errorf("%s: ungültige adresse %q: %w", field, r, err)
		}
		if !sendAllowed(a.Address) {
			return nil, fmt.Errorf("%s: %q liegt außerhalb der Versand-Allowlist (COVEY_EMAIL_SEND_DOMAINS)", field, a.Address)
		}
		out = append(out, a.Address)
	}
	return out, nil
}

// sanitizeHeader entfernt Zeilenumbrüche aus Header-Werten — die einzige
// Stelle, an der Nutzer-Text (Betreff) in Header gelangt.
func sanitizeHeader(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}
