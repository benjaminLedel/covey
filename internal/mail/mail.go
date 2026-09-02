// Package mail is how the control plane sends a message of its own.
//
// It is deliberately small. Two kinds of mail leave this platform and they
// have nothing to do with each other:
//
//   - An AGENT's mail. Its mailbox, its threading headers, its reply to a
//     customer, brokered per call through the email target plugin. That lives
//     in the plugin pack and stays there.
//   - The INSTALLATION's mail. A verification link, a password reset, a
//     notification that something is waiting. From the instance, to one person,
//     no thread, no attachment.
//
// FR-002 proposed extracting one message builder for both. That is not
// available: the dependency graph is acyclic and nothing depends on covey
// (CLAUDE.md) — the pack cannot import this package, and `internal/` would
// forbid it if it tried. Sharing would mean moving the builder into the plugin
// SDK and thereby making the message format part of the contract third parties
// build against. For two callers with different requirements that price is too
// high, so the duplication here is deliberate and this comment is the note
// that says so.
package mail

import (
	"context"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Message is one transactional mail: one recipient, one subject, a plain-text
// body — and, where the sender built one, an HTML rendering of the same
// content beside it (html.go).
//
// The text is not optional and never a summary of the HTML: it is the mail
// for every client that shows no HTML, and it is what the HTML is built from.
// Nothing in the HTML part is loaded from anywhere, so a client that blocks
// remote content still shows it whole.
type Message struct {
	To      string
	Subject string
	Body    string
	HTML    string
}

// Sender delivers a message. The port exists so a test can count mails
// instead of running an SMTP server, and so the second implementation
// (someone's transactional-mail API) is a package, not a rewrite.
type Sender interface {
	Send(ctx context.Context, m Message) error
	// Configured says whether sending can be attempted at all. Callers use it
	// to fail early with a clear reason instead of producing an account whose
	// verification link nobody could ever send.
	Configured(ctx context.Context) bool
}

// build serialises the message as RFC-5322 text: UTF-8 subject Q-encoded, body
// quoted-printable. `from` is the rendered From header, `fromAddr` the bare
// address it was built from — the Message-ID's domain comes from that one, not
// from the display name in front of it.
func build(from, fromAddr string, m Message, now time.Time) []byte {
	var b strings.Builder
	header := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	_, domain, _ := strings.Cut(fromAddr, "@")
	header("From", sanitizeHeader(from))
	header("To", sanitizeHeader(m.To))
	header("Subject", mime.QEncoding.Encode("utf-8", sanitizeHeader(m.Subject)))
	header("Date", now.Format(time.RFC1123Z))
	header("Message-ID", fmt.Sprintf("<%s@%s>", uuid.NewString(), domain))
	// RFC 3834. Without it an out-of-office reply comes back to the sender
	// address, and on a notification mailbox that is a loop with two
	// participants and no exit.
	header("Auto-Submitted", "auto-generated")
	header("MIME-Version", "1.0")
	if m.HTML == "" {
		header("Content-Type", "text/plain; charset=utf-8")
		header("Content-Transfer-Encoding", "quoted-printable")
		b.WriteString("\r\n")
		writeQP(&b, m.Body)
		return []byte(b.String())
	}
	// multipart/alternative, text first: the order tells the client which
	// part is the fallback and which the preferred rendering.
	boundary := "=_covey_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	header("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	b.WriteString("\r\n")
	for _, part := range []struct{ ctype, body string }{
		{"text/plain", m.Body},
		{"text/html", m.HTML},
	} {
		b.WriteString("--" + boundary + "\r\n")
		header("Content-Type", part.ctype+"; charset=utf-8")
		header("Content-Transfer-Encoding", "quoted-printable")
		b.WriteString("\r\n")
		writeQP(&b, part.body)
		b.WriteString("\r\n")
	}
	b.WriteString("--" + boundary + "--\r\n")
	return []byte(b.String())
}

func writeQP(b *strings.Builder, body string) {
	qp := quotedprintable.NewWriter(b)
	qp.Write([]byte(body))
	qp.Close()
}

// sanitizeHeader strips line breaks out of header values. The subject is the
// one place where text somebody else wrote reaches a header, and a smuggled-in
// CRLF there is a second header of the attacker's choosing.
func sanitizeHeader(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}

// address validates a recipient and returns the bare address.
func address(field, raw string) (string, error) {
	a, err := mail.ParseAddress(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("%s: invalid address %q: %w", field, raw, err)
	}
	return a.Address, nil
}
