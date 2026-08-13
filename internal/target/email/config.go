package email

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

// Configuration of the email plugin from the brokered secret pair. The broker
// knows exactly two secrets per system (email_url + email_token). The normal
// case is the shorthand — mail server, address, password:
//
//	email_url   = mail.example.com
//	email_token = agent@example.com:app-password
//
// The shorthand expands to the standard setup: IMAP with TLS on 993 and SMTP
// submission with STARTTLS on 587 on the same host. Differing hosts, ports or
// TLS modes are encoded by email_url as BOTH endpoints:
//
//	email_url   = imaps://imap.example.com:993 smtp://smtp.example.com:587
//
// Schemes: imaps (TLS, default port 993), imap (STARTTLS, 143), smtps (TLS,
// 465), smtp (STARTTLS, 587). For tests/demos additionally imap+insecure /
// smtp+insecure (plaintext, default ports 143/25). The sender address is the
// username; if the login differs from the mail address, ?from=agent@example.com
// on the SMTP URL sets it explicitly.

// TLS mode of a connection.
const (
	tlsImplicit = "tls"      // TLS from the first byte (imaps/smtps)
	tlsStartTLS = "starttls" // plaintext handshake, then STARTTLS (imap/smtp)
	tlsNone     = "none"     // plaintext — tests/demos only (…+insecure)
)

// Config is the parsed connection configuration of both endpoints.
type Config struct {
	IMAPAddr string // host:port
	IMAPHost string // hostname for TLS SNI
	IMAPTLS  string
	SMTPAddr string
	SMTPHost string
	SMTPTLS  string
	Username string
	Password string
	From     string // sender address (default: username)
}

// ParseConfig decomposes the brokered credential into the mail configuration.
func ParseConfig(cred target.Credential) (Config, error) {
	var cfg Config
	raw := strings.TrimSpace(strings.NewReplacer(";", " ", ",", " ").Replace(cred.BaseURL))
	if raw != "" && !strings.Contains(raw, "://") {
		// Shorthand: only the mail server host → standard setup on that same
		// host (IMAP TLS 993 + SMTP submission/STARTTLS 587). Ports, TLS modes
		// or separate hosts need the explicit URL form.
		if strings.ContainsAny(raw, ": /") {
			return Config{}, fmt.Errorf("email_url: %q — the shorthand is ONLY the mail server host (e.g. %q); differing ports/TLS modes have to be given as URLs (%q)",
				raw, "mail.example.com", "imaps://imap.example.com:993 smtp://smtp.example.com:587")
		}
		raw = "imaps://" + raw + " smtp://" + raw
	}
	for _, part := range strings.Fields(raw) {
		u, err := url.Parse(part)
		if err != nil {
			return Config{}, fmt.Errorf("email_url: %q not parsable: %w", part, err)
		}
		host := u.Hostname()
		if host == "" {
			return Config{}, fmt.Errorf("email_url: %q without host", part)
		}
		var mode, defPort string
		var isIMAP bool
		switch u.Scheme {
		case "imaps":
			isIMAP, mode, defPort = true, tlsImplicit, "993"
		case "imap":
			isIMAP, mode, defPort = true, tlsStartTLS, "143"
		case "imap+insecure":
			isIMAP, mode, defPort = true, tlsNone, "143"
		case "smtps":
			mode, defPort = tlsImplicit, "465"
		case "smtp":
			mode, defPort = tlsStartTLS, "587"
		case "smtp+insecure":
			mode, defPort = tlsNone, "25"
		default:
			return Config{}, fmt.Errorf("email_url: unknown scheme %q (expected imap[s]/smtp[s])", u.Scheme)
		}
		port := u.Port()
		if port == "" {
			port = defPort
		}
		if isIMAP {
			cfg.IMAPAddr, cfg.IMAPHost, cfg.IMAPTLS = net.JoinHostPort(host, port), host, mode
		} else {
			cfg.SMTPAddr, cfg.SMTPHost, cfg.SMTPTLS = net.JoinHostPort(host, port), host, mode
			if from := u.Query().Get("from"); from != "" {
				cfg.From = from
			}
		}
	}
	if cfg.IMAPAddr == "" || cfg.SMTPAddr == "" {
		return Config{}, fmt.Errorf("email_url has to contain an IMAP AND an SMTP endpoint, e.g. %q",
			"imaps://imap.example.com:993 smtp://smtp.example.com:587")
	}
	user, pass, ok := strings.Cut(cred.Token, ":")
	if !ok || user == "" || pass == "" {
		return Config{}, fmt.Errorf("email_token has to be %q", "user:password")
	}
	cfg.Username, cfg.Password = user, pass
	if cfg.From == "" {
		cfg.From = user
	}
	if !strings.Contains(cfg.From, "@") {
		return Config{}, fmt.Errorf("sender address unknown: the login %q is not a mail address — append ?from=agent@example.com to the SMTP URL", cfg.From)
	}
	return cfg, nil
}

// Operational configuration from ENV (12-factor, as with the GitLab plugin).
// Everything has safe defaults — a field that is not set restricts nothing.

// sendAllowed checks a recipient address against the allowlist from
// COVEY_EMAIL_SEND_DOMAINS (domains or full addresses, comma separated).
// Empty/unset → no restriction. The filter takes effect daemon-side before
// every SMTP delivery — in addition to the central guard-rails.
func sendAllowed(addr string) bool {
	allow := parseSet(os.Getenv("COVEY_EMAIL_SEND_DOMAINS"))
	if len(allow) == 0 {
		return true
	}
	a := strings.ToLower(strings.TrimSpace(addr))
	if allow[a] {
		return true
	}
	if _, domain, ok := strings.Cut(a, "@"); ok && allow[domain] {
		return true
	}
	return false
}

// maxAttachmentBytes is the upper bound for a single attachment materialized
// into the sandbox. Default 25 MB, overridable via
// COVEY_EMAIL_ATTACHMENT_MAX_MB (1 up to 1024 MB). Larger values are clamped,
// anything unreadable stays at the default — both with one line in the log, see
// target.MaxBytesFromEnv.
func maxAttachmentBytes() int64 {
	return target.MaxBytesFromEnv("COVEY_EMAIL_ATTACHMENT_MAX_MB", 25, 1024)
}

// senderInScope checks a sender address against the intake allowlist from
// COVEY_EMAIL_INTAKE_ADDRESSES (domains or full addresses). Empty → every
// sender is in scope. Used by list_unread/list_messages — mail outside the
// allowlist never shows up in the working set in the first place.
func senderInScope(addr string) bool {
	allow := parseSet(os.Getenv("COVEY_EMAIL_INTAKE_ADDRESSES"))
	if len(allow) == 0 {
		return true
	}
	a := strings.ToLower(strings.TrimSpace(addr))
	if allow[a] {
		return true
	}
	if _, domain, ok := strings.Cut(a, "@"); ok && allow[domain] {
		return true
	}
	return false
}

// parseSet decomposes a comma separated ENV list into a set of lowercased,
// trimmed values. Empty entries are discarded.
func parseSet(raw string) map[string]bool {
	out := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		v := strings.ToLower(strings.TrimSpace(part))
		if v != "" {
			out[v] = true
		}
	}
	return out
}
