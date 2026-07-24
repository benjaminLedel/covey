package email

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"covey/internal/target"
)

// Konfiguration des E-Mail-Plugins aus dem gebrokerten Secret-Paar. Der
// Broker kennt pro System genau zwei Secrets (email_url + email_token).
// Der Normalfall ist die Kurzform — Mailserver, Adresse, Passwort:
//
//	email_url   = mail.example.com
//	email_token = agent@example.com:app-passwort
//
// Die Kurzform expandiert zum Standard-Setup IMAP mit TLS auf 993 und
// SMTP-Submission mit STARTTLS auf 587 auf demselben Host. Abweichende
// Hosts, Ports oder TLS-Modi kodiert email_url als BEIDE Endpunkte:
//
//	email_url   = imaps://imap.example.com:993 smtp://smtp.example.com:587
//
// Schemata: imaps (TLS, Default-Port 993), imap (STARTTLS, 143), smtps
// (TLS, 465), smtp (STARTTLS, 587). Für Tests/Demos zusätzlich
// imap+insecure / smtp+insecure (Klartext, Default-Ports 143/25).
// Absender-Adresse ist der Benutzername; weicht der Login von der
// Mail-Adresse ab, setzt ?from=agent@example.com an der SMTP-URL sie
// explizit.

// TLS-Modus einer Verbindung.
const (
	tlsImplicit = "tls"      // TLS ab dem ersten Byte (imaps/smtps)
	tlsStartTLS = "starttls" // Klartext-Handshake, dann STARTTLS (imap/smtp)
	tlsNone     = "none"     // Klartext — nur Tests/Demos (…+insecure)
)

// Config ist die geparste Verbindungs-Konfiguration beider Endpunkte.
type Config struct {
	IMAPAddr string // host:port
	IMAPHost string // Hostname für TLS-SNI
	IMAPTLS  string
	SMTPAddr string
	SMTPHost string
	SMTPTLS  string
	Username string
	Password string
	From     string // Absender-Adresse (Default: Username)
}

// ParseConfig zerlegt das gebrokerte Credential in die Mail-Konfiguration.
func ParseConfig(cred target.Credential) (Config, error) {
	var cfg Config
	raw := strings.TrimSpace(strings.NewReplacer(";", " ", ",", " ").Replace(cred.BaseURL))
	if raw != "" && !strings.Contains(raw, "://") {
		// Kurzform: nur der Mailserver-Host → Standard-Setup auf demselben
		// Host (IMAP-TLS 993 + SMTP-Submission/STARTTLS 587). Ports, TLS-Modi
		// oder getrennte Hosts brauchen die explizite URL-Form.
		if strings.ContainsAny(raw, ": /") {
			return Config{}, fmt.Errorf("email_url: %q — die Kurzform ist NUR der Mailserver-Host (z. B. %q); abweichende Ports/TLS-Modi als URLs angeben (%q)",
				raw, "mail.example.com", "imaps://imap.example.com:993 smtp://smtp.example.com:587")
		}
		raw = "imaps://" + raw + " smtp://" + raw
	}
	for _, part := range strings.Fields(raw) {
		u, err := url.Parse(part)
		if err != nil {
			return Config{}, fmt.Errorf("email_url: %q nicht parsebar: %w", part, err)
		}
		host := u.Hostname()
		if host == "" {
			return Config{}, fmt.Errorf("email_url: %q ohne Host", part)
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
			return Config{}, fmt.Errorf("email_url: unbekanntes Schema %q (erwartet imap[s]/smtp[s])", u.Scheme)
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
		return Config{}, fmt.Errorf("email_url muss IMAP- UND SMTP-Endpoint enthalten, z. B. %q",
			"imaps://imap.example.com:993 smtp://smtp.example.com:587")
	}
	user, pass, ok := strings.Cut(cred.Token, ":")
	if !ok || user == "" || pass == "" {
		return Config{}, fmt.Errorf("email_token muss %q sein", "benutzer:passwort")
	}
	cfg.Username, cfg.Password = user, pass
	if cfg.From == "" {
		cfg.From = user
	}
	if !strings.Contains(cfg.From, "@") {
		return Config{}, fmt.Errorf("absender-adresse unbekannt: Login %q ist keine Mail-Adresse — ?from=agent@example.com an die SMTP-URL hängen", cfg.From)
	}
	return cfg, nil
}

// Betriebs-Konfiguration aus ENV (12-Factor, wie beim GitLab-Plugin). Alles
// hat sichere Defaults — ein nicht gesetztes Feld schränkt nichts ein.

// sendAllowed prüft eine Empfänger-Adresse gegen die Allowlist aus
// COVEY_EMAIL_SEND_DOMAINS (Domains oder volle Adressen, kommasepariert).
// Leer/ungesetzt → keine Einschränkung. Der Filter greift daemon-seitig vor
// jedem SMTP-Versand — zusätzlich zu den zentralen Guard-Rails.
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

// senderInScope prüft eine Absender-Adresse gegen die Intake-Allowlist aus
// COVEY_EMAIL_INTAKE_ADDRESSES (Domains oder volle Adressen). Leer → alle
// Absender im Scope. Genutzt von list_unread/list_messages — Mails außerhalb
// der Allowlist tauchen gar nicht erst im Arbeitsvorrat auf.
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

// parseSet zerlegt eine kommaseparierte ENV-Liste in ein Set
// kleingeschriebener, getrimmter Werte. Leere Einträge werden verworfen.
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
