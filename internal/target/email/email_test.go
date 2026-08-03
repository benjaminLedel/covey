package email

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
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

func TestParseConfigShorthand(t *testing.T) {
	cfg, err := ParseConfig(target.Credential{
		BaseURL: "  mail.example.com  ", // nur der Host → Standard-Setup
		Token:   "agent@example.com:pw",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IMAPAddr != "mail.example.com:993" || cfg.IMAPTLS != tlsImplicit {
		t.Errorf("imap: %q/%q", cfg.IMAPAddr, cfg.IMAPTLS)
	}
	if cfg.SMTPAddr != "mail.example.com:587" || cfg.SMTPTLS != tlsStartTLS {
		t.Errorf("smtp: %q/%q", cfg.SMTPAddr, cfg.SMTPTLS)
	}
	if cfg.From != "agent@example.com" {
		t.Errorf("from: %q", cfg.From)
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
		{BaseURL: "mail.example.com:993", Token: "u@x.de:p"},              // Kurzform mit Port
		{BaseURL: "imap.example.com smtp.example.com", Token: "u@x.de:p"}, // Kurzform mit zwei Hosts
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

func TestHasWork(t *testing.T) {
	imapAddr := newMemIMAP(t, testUser, "pw")
	smtp := newFakeSMTP(t)
	cred := testCred(t, imapAddr, smtp.ln.Addr().String())

	check := func(want bool, msg string) {
		t.Helper()
		has, err := (System{}).HasWork(context.Background(), cred)
		if err != nil {
			t.Fatal(err)
		}
		if has != want {
			t.Errorf("%s: has=%v, erwartet %v", msg, has, want)
		}
	}

	check(false, "leeres postfach")
	appendMail(t, imapAddr, testUser, "pw",
		"From: alt@example.com\r\nTo: agent@example.com\r\nSubject: Erledigt\r\n\r\nx\r\n", true)
	check(false, "nur gelesene mails")
	appendMail(t, imapAddr, testUser, "pw",
		"From: agent@example.com\r\nTo: agent@example.com\r\nSubject: Echo\r\n\r\nx\r\n", false)
	check(false, "eigene mail zählt nicht (echo-schutz)")
	appendMail(t, imapAddr, testUser, "pw",
		"From: kunde@example.com\r\nTo: agent@example.com\r\nSubject: Frage\r\n\r\nx\r\n", false)
	check(true, "ungelesene kundenmail")

	// Intake-Allowlist greift auch im Vorab-Check.
	t.Setenv("COVEY_EMAIL_INTAKE_ADDRESSES", "partner.de")
	check(false, "kundenmail außerhalb der intake-allowlist")
}

// --- Anhänge --------------------------------------------------------------

// multipartMail baut eine Roh-Mail mit Text-Teil und base64-kodierten
// Anhängen (Name → Content-Type/Inhalt in der angegebenen Reihenfolge).
func multipartMail(subject string, atts ...[3]string) string {
	var b strings.Builder
	b.WriteString("From: kunde@example.com\r\nTo: agent@example.com\r\nSubject: " + subject +
		"\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"grenze\"\r\n\r\n")
	b.WriteString("--grenze\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nSiehe Anhang.\r\n")
	for _, at := range atts {
		name, ct, content := at[0], at[1], at[2]
		fmt.Fprintf(&b, "--grenze\r\nContent-Type: %s\r\nContent-Disposition: attachment; filename=%q\r\n"+
			"Content-Transfer-Encoding: base64\r\n\r\n%s\r\n",
			ct, name, base64.StdEncoding.EncodeToString([]byte(content)))
	}
	b.WriteString("--grenze--\r\n")
	return b.String()
}

func TestFindAttachment(t *testing.T) {
	raw := []byte(multipartMail("Zwei Anhänge",
		[3]string{"rechnung.pdf", "application/pdf", "%PDF-1.4 fake"},
		[3]string{"logo.png", "image/png", "PNGDATA"},
		[3]string{"rechnung.pdf", "text/plain", "die zweite"},
	))

	// Treffer inkl. Content-Type und dekodierten Bytes.
	name, ct, data, err := findAttachment(raw, "logo.png", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if name != "logo.png" || ct != "image/png" || string(data) != "PNGDATA" {
		t.Fatalf("treffer: %q/%q/%q", name, ct, data)
	}

	// Gleichnamige Anhänge: der erste in MIME-Reihenfolge gewinnt.
	if _, ct, data, err = findAttachment(raw, "rechnung.pdf", 1<<20); err != nil {
		t.Fatal(err)
	} else if ct != "application/pdf" || string(data) != "%PDF-1.4 fake" {
		t.Fatalf("bei gleichnamigen anhängen muss der erste gewinnen: %q/%q", ct, data)
	}

	// Größenlimit: über dem Limit wird abgebrochen, nichts geliefert.
	if _, _, data, err = findAttachment(raw, "rechnung.pdf", 4); err == nil {
		t.Fatalf("anhang über dem limit muss scheitern (bekam %d bytes)", len(data))
	} else if !strings.Contains(err.Error(), "größer als") {
		t.Fatalf("fehlertext: %v", err)
	}

	// Unbekannter Name → Fehler, der die vorhandenen Namen nennt.
	if _, _, _, err = findAttachment(raw, "gibtsnicht.txt", 1<<20); err == nil {
		t.Fatal("unbekannter anhang muss scheitern")
	} else if !strings.Contains(err.Error(), "rechnung.pdf") {
		t.Fatalf("fehlertext nennt die vorhandenen namen nicht: %v", err)
	}

	// Mail ganz ohne Anhänge → eigener, klarer Fehler (kein Panic).
	ohne := []byte("From: kunde@example.com\r\nSubject: Nur Text\r\n\r\nkein anhang\r\n")
	if _, _, _, err = findAttachment(ohne, "x.pdf", 1<<20); err == nil {
		t.Fatal("mail ohne anhänge muss scheitern")
	} else if !strings.Contains(err.Error(), "keine anhänge") {
		t.Fatalf("fehlertext: %v", err)
	}
}

// bsPart baut einen BODYSTRUCTURE-Teil, wie ihn ein Server für einen Anhang
// bzw. den Fließtext meldet. Ohne Disposition steht der Name — wie bei älteren
// Mailern — im Content-Type.
func bsPart(typ, sub, disp, filename string, size uint32) *imap.BodyStructureSinglePart {
	p := &imap.BodyStructureSinglePart{Type: typ, Subtype: sub, Size: size, Encoding: "base64"}
	switch {
	case disp != "":
		p.Extended = &imap.BodyStructureSinglePartExt{Disposition: &imap.BodyStructureDisposition{
			Value: disp, Params: map[string]string{"filename": filename},
		}}
	case filename != "":
		p.Params = map[string]string{"name": filename}
	}
	return p
}

func TestFindAttachmentPart(t *testing.T) {
	bs := &imap.BodyStructureMultiPart{Subtype: "mixed", Children: []imap.BodyStructure{
		bsPart("text", "plain", "", "", 20),                              // 1 Fließtext
		bsPart("image", "png", "inline", "logo.png", 100),                // 2 inline
		bsPart("application", "pdf", "attachment", "rechnung.pdf", 2048), // 3
		bsPart("text", "plain", "attachment", "rechnung.pdf", 99),        // 4 gleicher Name
		bsPart("application", "zip", "", "archiv.zip", 7),                // 5 ohne Disposition
		bsPart("application", "pdf", "attachment", "=?UTF-8?Q?Gr=C3=BC=C3=9Fe.pdf?=", 42),
	}}

	// Treffer mit Pfad und kodierter Größe; bei gleichnamigen Anhängen gewinnt
	// der erste in MIME-Reihenfolge (wie in findAttachment).
	p := findAttachmentPart(bs, "rechnung.pdf")
	if p == nil {
		t.Fatal("rechnung.pdf nicht gefunden")
	}
	if fmt.Sprint(p.path) != "[3]" || p.size != 2048 {
		t.Errorf("teil: %+v", p)
	}

	// Ohne Content-Disposition zählt ein Nicht-Text-Teil trotzdem als Anhang —
	// dieselbe Einordnung wie in go-message/mail.
	if p := findAttachmentPart(bs, "archiv.zip"); p == nil || fmt.Sprint(p.path) != "[5]" {
		t.Errorf("archiv.zip: %+v", p)
	}

	// RFC-2047-kodierte Dateinamen werden vor dem Vergleich dekodiert.
	if p := findAttachmentPart(bs, "Grüße.pdf"); p == nil || fmt.Sprint(p.path) != "[6]" {
		t.Errorf("kodierter dateiname: %+v", p)
	}

	// Pfad-Traversal im Namen wird auch hier auf den Basename reduziert.
	if p := findAttachmentPart(bs, "../../rechnung.pdf"); p == nil || fmt.Sprint(p.path) != "[3]" {
		t.Errorf("basename-vergleich: %+v", p)
	}

	// Inline-Teile sind keine Anhänge — get_message listet sie auch nicht.
	// Unbekannter Name und fehlende Struktur führen in den Fallback (nil).
	for _, tc := range []struct {
		bs   imap.BodyStructure
		name string
	}{
		{bs, "logo.png"},
		{bs, "gibtsnicht.txt"},
		{nil, "rechnung.pdf"},
	} {
		if p := findAttachmentPart(tc.bs, tc.name); p != nil {
			t.Errorf("%q: kein treffer erwartet, bekam %+v", tc.name, p)
		}
	}
}

func TestMaxAttachmentBytes(t *testing.T) {
	t.Setenv("COVEY_EMAIL_ATTACHMENT_MAX_MB", "")
	if got := maxAttachmentBytes(); got != 25<<20 {
		t.Errorf("default: %d", got)
	}
	t.Setenv("COVEY_EMAIL_ATTACHMENT_MAX_MB", "2")
	if got := maxAttachmentBytes(); got != 2<<20 {
		t.Errorf("override: %d", got)
	}
	// Unlesbares und Nicht-Positives lässt den Default stehen (fail-closed).
	for _, v := range []string{"0", "-1", "viel"} {
		t.Setenv("COVEY_EMAIL_ATTACHMENT_MAX_MB", v)
		if got := maxAttachmentBytes(); got != 25<<20 {
			t.Errorf("wert %q: %d, erwartet default", v, got)
		}
	}
	// Zu große Werte werden auf das Maximum geklemmt statt auf den Default
	// zurückzufallen. Wer 2048 einträgt, will offensichtlich viel und nicht die
	// Voreinstellung — ein 80-fach kleineres Limit ohne ein Wort war die
	// unfreundlichste aller Antworten (GitHub #2, Punkt 3). Der Schutz vor dem
	// Überlauf beim Umrechnen in Bytes bleibt: geklemmt wird auf 1024 MB.
	for _, v := range []string{"2048", "8796093022208"} {
		t.Setenv("COVEY_EMAIL_ATTACHMENT_MAX_MB", v)
		if got := maxAttachmentBytes(); got != 1024<<20 {
			t.Errorf("wert %q: %d, erwartet 1024 MB", v, got)
		}
	}
}

func TestExecuteGetAttachment(t *testing.T) {
	imapAddr := newMemIMAP(t, testUser, "pw")
	smtp := newFakeSMTP(t)
	cred := testCred(t, imapAddr, smtp.ln.Addr().String())

	appendMail(t, imapAddr, testUser, "pw", multipartMail("Rechnung",
		[3]string{"rechnung.pdf", "application/pdf", "%PDF-1.4 fake"},
		[3]string{"../../etc/passwd", "text/plain", "boese"},
	), false)

	work := t.TempDir()
	ctx := target.WithWorkdir(context.Background(), work)
	get := func(params string) (AttachmentResult, error) {
		res, err := (System{}).Execute(ctx, "get_attachment", json.RawMessage(params), cred)
		if err != nil {
			return AttachmentResult{}, err
		}
		return res.(AttachmentResult), nil
	}

	unread := exec(t, cred, "list_unread", `{}`).([]MessageSummary)
	if len(unread) != 1 {
		t.Fatalf("list_unread: %+v", unread)
	}
	uid := unread[0].UID

	// get_message listet die Namen weiterhin (unverändertes Verhalten).
	msg := exec(t, cred, "get_message", fmt.Sprintf(`{"uid":%d}`, uid)).(*Message)
	if strings.Join(msg.Attachments, ",") != "rechnung.pdf,../../etc/passwd" {
		t.Fatalf("attachment-namen in get_message: %v", msg.Attachments)
	}

	// Anhang landet in <workdir>/attachments/ und ist byte-gleich.
	res, err := get(fmt.Sprintf(`{"uid":%d,"name":"rechnung.pdf"}`, uid))
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != filepath.Join(work, "attachments", "rechnung.pdf") ||
		res.ContentType != "application/pdf" || res.Bytes != 13 {
		t.Fatalf("ergebnis: %+v", res)
	}
	if data, _ := os.ReadFile(res.Path); string(data) != "%PDF-1.4 fake" {
		t.Fatalf("datei-inhalt: %q", data)
	}

	// Wie get_message setzt auch get_attachment KEIN Gelesen-Flag (BODY.PEEK).
	if still := exec(t, cred, "list_unread", `{}`).([]MessageSummary); len(still) != 1 || still[0].Seen {
		t.Errorf("get_attachment darf das gelesen-flag nicht setzen: %+v", still)
	}

	// Pfad-Traversal im Anhang-Namen wird auf den Basename reduziert.
	res, err = get(fmt.Sprintf(`{"uid":%d,"name":"../../etc/passwd"}`, uid))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(res.Path) != filepath.Join(work, "attachments") || res.Filename != "passwd" {
		t.Fatalf("pfad-traversal nicht neutralisiert: %+v", res)
	}

	// Unbekannter Anhang, unbekannte UID, fehlender Name → Fehler.
	for _, params := range []string{
		fmt.Sprintf(`{"uid":%d,"name":"gibtsnicht.txt"}`, uid),
		`{"uid":999999,"name":"rechnung.pdf"}`,
		fmt.Sprintf(`{"uid":%d}`, uid),
		`{"name":"rechnung.pdf"}`,
	} {
		if _, err := get(params); err == nil {
			t.Errorf("%s: fehler erwartet", params)
		}
	}

	// Ohne Sandbox-Workdir → Fehler (Bytes brauchen ein Ziel).
	if _, err := (System{}).Execute(context.Background(), "get_attachment",
		json.RawMessage(fmt.Sprintf(`{"uid":%d,"name":"rechnung.pdf"}`, uid)), cred); err == nil {
		t.Error("ohne workdir muss get_attachment scheitern")
	}
}

func TestExecuteGetAttachmentGroessenlimit(t *testing.T) {
	imapAddr := newMemIMAP(t, testUser, "pw")
	smtp := newFakeSMTP(t)
	cred := testCred(t, imapAddr, smtp.ln.Addr().String())

	appendMail(t, imapAddr, testUser, "pw", multipartMail("Dickes Ding",
		[3]string{"gross.bin", "application/octet-stream", strings.Repeat("x", 2<<20)},
	), false)

	t.Setenv("COVEY_EMAIL_ATTACHMENT_MAX_MB", "1")
	work := t.TempDir()
	ctx := target.WithWorkdir(context.Background(), work)
	uid := exec(t, cred, "list_unread", `{}`).([]MessageSummary)[0].UID

	_, err := (System{}).Execute(ctx, "get_attachment",
		json.RawMessage(fmt.Sprintf(`{"uid":%d,"name":"gross.bin"}`, uid)), cred)
	if err == nil {
		t.Fatal("anhang über dem limit muss scheitern")
	}
	// Fail-closed: keine angefangene Datei in der Sandbox.
	if _, statErr := os.Stat(filepath.Join(work, "attachments", "gross.bin")); !os.IsNotExist(statErr) {
		t.Errorf("teil-datei trotz limit-abbruch geschrieben (%v)", statErr)
	}
}

// TestGetAttachmentHoltNurDenGefragtenTeil belegt, dass der gesuchte Anhang
// über die BODYSTRUCTURE einzeln geholt wird: die Mail als Ganzes liegt über
// dem Speicher-Budget (dem Vierfachen des Limits) — käme sie komplett, müsste
// auch der kleine Anhang daran scheitern.
func TestGetAttachmentHoltNurDenGefragtenTeil(t *testing.T) {
	imapAddr := newMemIMAP(t, testUser, "pw")
	smtp := newFakeSMTP(t)
	cred := testCred(t, imapAddr, smtp.ln.Addr().String())

	appendMail(t, imapAddr, testUser, "pw", multipartMail("Dickes Paket",
		[3]string{"klein.txt", "text/plain", "nur ein paar bytes"},
		[3]string{"gross.bin", "application/octet-stream", strings.Repeat("x", 4<<20)},
	), false)

	t.Setenv("COVEY_EMAIL_ATTACHMENT_MAX_MB", "1")
	work := t.TempDir()
	ctx := target.WithWorkdir(context.Background(), work)
	uid := exec(t, cred, "list_unread", `{}`).([]MessageSummary)[0].UID

	res, err := (System{}).Execute(ctx, "get_attachment",
		json.RawMessage(fmt.Sprintf(`{"uid":%d,"name":"klein.txt"}`, uid)), cred)
	if err != nil {
		t.Fatalf("kleiner anhang aus großer mail: %v", err)
	}
	if got := res.(AttachmentResult); got.Filename != "klein.txt" || got.Bytes != 18 {
		t.Errorf("ergebnis: %+v", got)
	}

	// Der große Anhang bleibt draußen — die BODYSTRUCTURE nennt seine Größe,
	// also fließen seine Bytes gar nicht erst.
	if _, err := (System{}).Execute(ctx, "get_attachment",
		json.RawMessage(fmt.Sprintf(`{"uid":%d,"name":"gross.bin"}`, uid)), cred); err == nil {
		t.Error("anhang über dem limit muss scheitern")
	}
	if _, statErr := os.Stat(filepath.Join(work, "attachments", "gross.bin")); !os.IsNotExist(statErr) {
		t.Errorf("teil-datei trotz limit-abbruch geschrieben (%v)", statErr)
	}
}

func TestExecuteUnbekannteAktion(t *testing.T) {
	cred := target.Credential{BaseURL: "imaps://a smtp://b", Token: "u@x.de:p"}
	if _, err := (System{}).Execute(context.Background(), "kaboom", json.RawMessage(`{}`), cred); err == nil {
		t.Fatal("fehler erwartet")
	}
}
