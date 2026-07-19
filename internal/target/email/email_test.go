package email

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"

	"covey/internal/target"
)

// --- Config ---------------------------------------------------------------

func TestParseConfig(t *testing.T) {
	cfg, err := ParseConfig(target.Credential{
		BaseURL: "imaps://imap.example.com smtp://mail.example.com:2525",
		Token:   "agent@example.com:ge:heim", // Passwort darf Doppelpunkte enthalten
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IMAPAddr != "imap.example.com:993" || cfg.IMAPTLS != tlsImplicit {
		t.Errorf("imap: %q/%q", cfg.IMAPAddr, cfg.IMAPTLS)
	}
	if cfg.SMTPAddr != "mail.example.com:2525" || cfg.SMTPTLS != tlsStartTLS {
		t.Errorf("smtp: %q/%q", cfg.SMTPAddr, cfg.SMTPTLS)
	}
	if cfg.Username != "agent@example.com" || cfg.Password != "ge:heim" || cfg.From != "agent@example.com" {
		t.Errorf("login: %q/%q/%q", cfg.Username, cfg.Password, cfg.From)
	}
}

func TestParseConfigFromOverride(t *testing.T) {
	cfg, err := ParseConfig(target.Credential{
		BaseURL: "imap+insecure://h:1143; smtp+insecure://h:1025?from=bot@example.com",
		Token:   "login-name:pw",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.From != "bot@example.com" || cfg.IMAPTLS != tlsNone || cfg.SMTPTLS != tlsNone {
		t.Errorf("cfg: %+v", cfg)
	}
}

func TestParseConfigErrors(t *testing.T) {
	cases := []target.Credential{
		{BaseURL: "imaps://imap.example.com", Token: "u@x.de:p"},          // SMTP fehlt
		{BaseURL: "smtp://mail.example.com", Token: "u@x.de:p"},           // IMAP fehlt
		{BaseURL: "imaps://a smtp://b", Token: "ohne-doppelpunkt"},        // Token-Format
		{BaseURL: "imaps://a smtp://b", Token: "login:pw"},                // From unbekannt
		{BaseURL: "https://a smtp://b", Token: "u@x.de:p"},                // falsches Schema
	}
	for i, cred := range cases {
		if _, err := ParseConfig(cred); err == nil {
			t.Errorf("fall %d: fehler erwartet", i)
		}
	}
}

func TestSendAllowlist(t *testing.T) {
	t.Setenv("COVEY_EMAIL_SEND_DOMAINS", "example.com, chef@partner.de")
	for addr, want := range map[string]bool{
		"kunde@example.com": true,
		"Chef@Partner.de":   true,
		"boese@evil.io":     false,
		"azubi@partner.de":  false,
	} {
		if got := sendAllowed(addr); got != want {
			t.Errorf("sendAllowed(%q) = %v, will %v", addr, got, want)
		}
	}
	t.Setenv("COVEY_EMAIL_SEND_DOMAINS", "")
	if !sendAllowed("wer@auch.immer") {
		t.Error("leere allowlist muss alles erlauben")
	}
}

// --- Nachrichtenbau -------------------------------------------------------

func TestBuildMessage(t *testing.T) {
	msg := string(buildMessage(outgoing{
		From: "agent@example.com", To: []string{"kunde@example.com"},
		Subject:    "Übläut\r\nX-Injected: ja",
		Body:       "Grüße aus dem Postfach",
		InReplyTo:  "<orig@example.com>",
		References: []string{"<a@x>", "<orig@example.com>"},
	}, time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)))
	for _, want := range []string{
		"From: agent@example.com\r\n",
		"To: kunde@example.com\r\n",
		"In-Reply-To: <orig@example.com>\r\n",
		"References: <a@x> <orig@example.com>\r\n",
		"Content-Transfer-Encoding: quoted-printable\r\n",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("header fehlt: %q", want)
		}
	}
	if strings.Contains(msg, "X-Injected: ja\r\n") {
		t.Error("header-injection über den betreff nicht neutralisiert")
	}
	if !strings.Contains(msg, "Gr=C3=BC=C3=9Fe") {
		t.Errorf("body nicht quoted-printable: %s", msg)
	}
}

func TestBuildReply(t *testing.T) {
	cfg := Config{From: "agent@example.com"}
	orig := &Message{
		MessageSummary: MessageSummary{From: "kunde@example.com", Subject: "Frage", MessageID: "<m1@x>"},
		To:             []string{"agent@example.com", "team@example.com"},
		Cc:             []string{"chefin@example.com"},
		InReplyTo:      []string{"<m0@x>"},
	}
	o, err := buildReply(cfg, orig, "Antwort", true)
	if err != nil {
		t.Fatal(err)
	}
	if o.To[0] != "kunde@example.com" || o.Subject != "Re: Frage" || o.InReplyTo != "<m1@x>" {
		t.Errorf("reply: %+v", o)
	}
	if strings.Join(o.Cc, ",") != "team@example.com,chefin@example.com" {
		t.Errorf("reply_all cc: %v (eigene adresse muss raus)", o.Cc)
	}
	if strings.Join(o.References, " ") != "<m0@x> <m1@x>" {
		t.Errorf("references: %v", o.References)
	}

	// Re:-Betreff nicht doppeln.
	orig.Subject = "RE: Frage"
	if o, _ = buildReply(cfg, orig, "x", false); o.Subject != "RE: Frage" {
		t.Errorf("re-präfix gedoppelt: %q", o.Subject)
	}

	// Echo-Schutz: Antwort an die eigene Adresse ist verboten.
	orig.MessageSummary.From = "agent@example.com"
	if _, err := buildReply(cfg, orig, "x", false); err == nil {
		t.Error("antwort an eigene adresse muss scheitern")
	}
}

func TestActionSubject(t *testing.T) {
	if s := (System{}).ActionSubject("send", nil); s != "email:send" {
		t.Errorf("subject: %q", s)
	}
}

// --- Ende-zu-Ende gegen In-Memory-IMAP + Fake-SMTP ------------------------

// newMemIMAP startet einen In-Memory-IMAP-Server (Klartext, für die
// imap+insecure-Test-Konfiguration) mit einem Benutzer und INBOX+Archiv.
func newMemIMAP(t *testing.T, user, pass string) string {
	t.Helper()
	mem := imapmemserver.New()
	u := imapmemserver.NewUser(user, pass)
	if err := u.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	if err := u.Create("Archiv", nil); err != nil {
		t.Fatal(err)
	}
	mem.AddUser(u)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String()
}

// appendMail legt eine Roh-Mail per IMAP-APPEND ins Postfach.
func appendMail(t *testing.T, addr, user, pass, raw string, seen bool) {
	t.Helper()
	c, err := imapclient.DialInsecure(addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Login(user, pass).Wait(); err != nil {
		t.Fatal(err)
	}
	opts := &imap.AppendOptions{}
	if seen {
		opts.Flags = []imap.Flag{imap.FlagSeen}
	}
	cmd := c.Append("INBOX", int64(len(raw)), opts)
	if _, err := cmd.Write([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	_ = c.Logout().Wait()
}

// fakeSMTP ist ein minimaler Klartext-SMTP-Server, der die eingelieferte
// Nachricht aufzeichnet — analog zum Fake-Zammad der Integrationstests.
type fakeSMTP struct {
	ln    net.Listener
	mu    sync.Mutex
	from  string
	rcpts []string
	data  string
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeSMTP{ln: ln}
	go s.serve()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *fakeSMTP) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *fakeSMTP) handle(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	fmt.Fprintf(conn, "220 fake ESMTP\r\n")
	var data bytes.Buffer
	inData := false
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		if inData {
			if strings.TrimRight(line, "\r\n") == "." {
				inData = false
				s.mu.Lock()
				s.data = data.String()
				s.mu.Unlock()
				fmt.Fprintf(conn, "250 OK\r\n")
			} else {
				data.WriteString(line)
			}
			continue
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			fmt.Fprintf(conn, "250-fake\r\n250 8BITMIME\r\n")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			s.mu.Lock()
			s.from = pathArg(line)
			s.mu.Unlock()
			fmt.Fprintf(conn, "250 OK\r\n")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			s.mu.Lock()
			s.rcpts = append(s.rcpts, pathArg(line))
			s.mu.Unlock()
			fmt.Fprintf(conn, "250 OK\r\n")
		case cmd == "DATA":
			inData = true
			fmt.Fprintf(conn, "354 go\r\n")
		case cmd == "QUIT":
			fmt.Fprintf(conn, "221 bye\r\n")
			return
		default:
			fmt.Fprintf(conn, "250 OK\r\n")
		}
	}
}

// pathArg extrahiert die Adresse aus "MAIL FROM:<a@b> PARAM=X" / "RCPT TO:<a@b>".
func pathArg(line string) string {
	if i, j := strings.Index(line, "<"), strings.Index(line, ">"); i >= 0 && j > i {
		return line[i+1 : j]
	}
	return strings.TrimSpace(line)
}

func (s *fakeSMTP) snapshot() (string, []string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.from, append([]string{}, s.rcpts...), s.data
}

const testUser = "agent@example.com"

func testCred(t *testing.T, imapAddr, smtpAddr string) target.Credential {
	t.Helper()
	return target.Credential{
		BaseURL: fmt.Sprintf("imap+insecure://%s smtp+insecure://%s", imapAddr, smtpAddr),
		Token:   testUser + ":pw",
	}
}

func exec(t *testing.T, cred target.Credential, action, params string) any {
	t.Helper()
	res, err := (System{}).Execute(context.Background(), action, json.RawMessage(params), cred)
	if err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	return res
}

func TestExecuteInboxRoundtrip(t *testing.T) {
	imapAddr := newMemIMAP(t, testUser, "pw")
	smtp := newFakeSMTP(t)
	cred := testCred(t, imapAddr, smtp.ln.Addr().String())

	appendMail(t, imapAddr, testUser, "pw",
		"From: Kunde <kunde@example.com>\r\nTo: agent@example.com\r\nSubject: Drucker kaputt\r\nMessage-ID: <t1@example.com>\r\nDate: Sat, 18 Jul 2026 10:00:00 +0200\r\n\r\nDer Drucker im 2. OG druckt nicht.\r\n", false)
	appendMail(t, imapAddr, testUser, "pw",
		"From: alt@example.com\r\nTo: agent@example.com\r\nSubject: Erledigt\r\n\r\nAlte Mail.\r\n", true)

	// list_unread: nur die ungelesene Mail.
	unread := exec(t, cred, "list_unread", `{}`).([]MessageSummary)
	if len(unread) != 1 || unread[0].From != "kunde@example.com" || unread[0].Seen {
		t.Fatalf("list_unread: %+v", unread)
	}
	uid := unread[0].UID

	// list_messages: beide.
	if all := exec(t, cred, "list_messages", `{}`).([]MessageSummary); len(all) != 2 {
		t.Fatalf("list_messages: %+v", all)
	}

	// get_message: Text extrahiert, Gelesen-Flag unangetastet.
	msg := exec(t, cred, "get_message", fmt.Sprintf(`{"uid":%d}`, uid)).(*Message)
	if !strings.Contains(msg.Body, "Drucker im 2. OG") || msg.MessageID != "<t1@example.com>" {
		t.Fatalf("get_message: %+v", msg)
	}
	if got := exec(t, cred, "list_unread", `{}`).([]MessageSummary); len(got) != 1 {
		t.Fatalf("get_message darf das gelesen-flag nicht setzen: %+v", got)
	}

	// reply: SMTP-Versand mit Threading-Headern, danach als gelesen markiert.
	exec(t, cred, "reply", fmt.Sprintf(`{"uid":%d,"body":"Wir kümmern uns."}`, uid))
	from, rcpts, data := smtp.snapshot()
	if from != testUser || len(rcpts) != 1 || rcpts[0] != "kunde@example.com" {
		t.Fatalf("smtp-umschlag: %q → %v", from, rcpts)
	}
	if !strings.Contains(data, "In-Reply-To: <t1@example.com>") ||
		!strings.Contains(data, "Subject: Re: Drucker kaputt") {
		t.Fatalf("reply-nachricht:\n%s", data)
	}
	if got := exec(t, cred, "list_unread", `{}`).([]MessageSummary); len(got) != 0 {
		t.Fatalf("reply muss als gelesen markieren: %+v", got)
	}

	// mark_unseen → wieder im Arbeitsvorrat; move → Archiv.
	exec(t, cred, "mark_unseen", fmt.Sprintf(`{"uid":%d}`, uid))
	if got := exec(t, cred, "list_unread", `{}`).([]MessageSummary); len(got) != 1 {
		t.Fatalf("mark_unseen: %+v", got)
	}
	exec(t, cred, "move", fmt.Sprintf(`{"uid":%d,"to_mailbox":"Archiv"}`, uid))
	if got := exec(t, cred, "list_unread", `{}`).([]MessageSummary); len(got) != 0 {
		t.Fatalf("nach move noch in INBOX: %+v", got)
	}
	if got := exec(t, cred, "list_messages", `{"mailbox":"Archiv"}`).([]MessageSummary); len(got) != 1 {
		t.Fatalf("archiv: %+v", got)
	}

	// list_mailboxes.
	boxes := exec(t, cred, "list_mailboxes", `{}`).([]string)
	if strings.Join(boxes, ",") != "Archiv,INBOX" {
		t.Fatalf("mailboxes: %v", boxes)
	}
}

func TestExecuteSend(t *testing.T) {
	imapAddr := newMemIMAP(t, testUser, "pw")
	smtp := newFakeSMTP(t)
	cred := testCred(t, imapAddr, smtp.ln.Addr().String())

	exec(t, cred, "send", `{"to":["kunde@example.com"],"cc":["chefin@example.com"],"subject":"Störung behoben","body":"Der Drucker läuft wieder."}`)
	from, rcpts, data := smtp.snapshot()
	if from != testUser || strings.Join(rcpts, ",") != "kunde@example.com,chefin@example.com" {
		t.Fatalf("umschlag: %q → %v", from, rcpts)
	}
	if !strings.Contains(data, "To: kunde@example.com\r\n") || !strings.Contains(data, "Cc: chefin@example.com\r\n") {
		t.Fatalf("nachricht:\n%s", data)
	}

	// Versand-Allowlist greift vor dem SMTP-Kontakt.
	t.Setenv("COVEY_EMAIL_SEND_DOMAINS", "example.com")
	if _, err := (System{}).Execute(context.Background(), "send",
		json.RawMessage(`{"to":["boese@evil.io"],"subject":"x","body":"y"}`), cred); err == nil {
		t.Fatal("send außerhalb der allowlist muss scheitern")
	}
}

func TestExecuteEchoUndIntakeFilter(t *testing.T) {
	imapAddr := newMemIMAP(t, testUser, "pw")
	smtp := newFakeSMTP(t)
	cred := testCred(t, imapAddr, smtp.ln.Addr().String())

	// Eigene Mail und ein fremder Absender außerhalb der Intake-Allowlist.
	appendMail(t, imapAddr, testUser, "pw",
		"From: agent@example.com\r\nTo: agent@example.com\r\nSubject: Notiz an mich\r\n\r\nx\r\n", false)
	appendMail(t, imapAddr, testUser, "pw",
		"From: spam@evil.io\r\nTo: agent@example.com\r\nSubject: Gewinnspiel\r\n\r\nx\r\n", false)
	appendMail(t, imapAddr, testUser, "pw",
		"From: kunde@example.com\r\nTo: agent@example.com\r\nSubject: Frage\r\nMessage-ID: <q@x>\r\n\r\nx\r\n", false)

	t.Setenv("COVEY_EMAIL_INTAKE_ADDRESSES", "example.com")
	unread := exec(t, cred, "list_unread", `{}`).([]MessageSummary)
	if len(unread) != 1 || unread[0].From != "kunde@example.com" {
		t.Fatalf("echo-/intake-filter: %+v", unread)
	}
}

func TestExecuteUnbekannteAktion(t *testing.T) {
	cred := target.Credential{BaseURL: "imaps://a smtp://b", Token: "u@x.de:p"}
	if _, err := (System{}).Execute(context.Background(), "kaboom", json.RawMessage(`{}`), cred); err == nil {
		t.Fatal("fehler erwartet")
	}
}
