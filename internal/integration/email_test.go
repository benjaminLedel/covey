package integration

import (
	"bufio"
	"bytes"
	"context"
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

	"covey/internal/backlog"
)

// TestEmailPlugin: das E-Mail-Zielsystem end-to-end durch den Stack — die
// Zwei-Secret-Konvention des Brokers (email_url kodiert IMAP- UND
// SMTP-Endpoint), ACCESS.md-Gate und Action-Proxy bis zum echten
// Protokoll-Roundtrip (In-Memory-IMAP, Fake-SMTP).
func TestEmailPlugin(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	const mailUser = "agent@example.com"
	imapAddr := startMemIMAP(t, mailUser, "pw")
	appendTestMail(t, imapAddr, mailUser, "pw",
		"From: Kunde <kunde@example.com>\r\nTo: agent@example.com\r\nSubject: Drucker kaputt\r\nMessage-ID: <t1@example.com>\r\n\r\nDer Drucker druckt nicht.\r\n")
	smtp := startFakeSMTP(t)

	// Aktivierung ist opt-in — die Test-Org schaltet das Built-in frei.
	if _, err := s.pool.Exec(ctx, `INSERT INTO target_plugins (org_id, name, kind, enabled)
		VALUES ($1,'email','builtin',TRUE)`, s.orgID); err != nil {
		t.Fatal(err)
	}

	agent, err := s.registry.Create(ctx, s.orgID, "mailer", "Mail-Agent", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md":   "# Mail-Agent",
		"ACCESS.md": "- system: email scope: read,write",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	s.secrets.Put(ctx, s.orgID, "email_url",
		fmt.Sprintf("imap+insecure://%s smtp+insecure://%s", imapAddr, smtp.addr()))
	s.secrets.Put(ctx, s.orgID, "email_token", mailUser+":pw")
	s.secrets.Assign(ctx, s.orgID, "email_url", agent.ID)
	s.secrets.Assign(ctx, s.orgID, "email_token", agent.ID)

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Posteingang sichten",
		`[mock:action email/list_unread {}] [mock:action email/reply {"uid":1,"body":"Wir kümmern uns um den Drucker."}] [mock:result Posteingang bearbeitet]`,
		"manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "mail-aufgabe done", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	// Die Antwort ging als echte SMTP-Einlieferung raus — mit Umschlag und
	// Threading-Headern aus der Original-Mail.
	from, rcpts, data := smtp.snapshot()
	if from != mailUser || strings.Join(rcpts, ",") != "kunde@example.com" {
		t.Fatalf("smtp-umschlag: %q → %v", from, rcpts)
	}
	if !strings.Contains(data, "Subject: Re: Drucker kaputt") ||
		!strings.Contains(data, "In-Reply-To: <t1@example.com>") {
		t.Fatalf("reply-nachricht unvollständig:\n%s", data)
	}
}

// startMemIMAP startet einen In-Memory-IMAP-Server (Klartext — passend zur
// imap+insecure-Test-Konfiguration des Plugins).
func startMemIMAP(t *testing.T, user, pass string) string {
	t.Helper()
	mem := imapmemserver.New()
	u := imapmemserver.NewUser(user, pass)
	if err := u.Create("INBOX", nil); err != nil {
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

// appendTestMail legt eine Roh-Mail per IMAP-APPEND ins Postfach (UID 1, 2, …).
func appendTestMail(t *testing.T, addr, user, pass, raw string) {
	t.Helper()
	c, err := imapclient.DialInsecure(addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Login(user, pass).Wait(); err != nil {
		t.Fatal(err)
	}
	cmd := c.Append("INBOX", int64(len(raw)), &imap.AppendOptions{})
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

// fakeSMTPServer nimmt eine Einlieferung im Klartext entgegen und zeichnet
// Umschlag + Nachricht auf — das SMTP-Gegenstück zum fakeZammad.
type fakeSMTPServer struct {
	ln    net.Listener
	mu    sync.Mutex
	from  string
	rcpts []string
	data  string
}

func startFakeSMTP(t *testing.T) *fakeSMTPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTPServer{ln: ln}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.handle(conn)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return f
}

func (f *fakeSMTPServer) addr() string { return f.ln.Addr().String() }

func (f *fakeSMTPServer) handle(conn net.Conn) {
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
				f.mu.Lock()
				f.data = data.String()
				f.mu.Unlock()
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
			f.mu.Lock()
			f.from = smtpPathArg(line)
			f.mu.Unlock()
			fmt.Fprintf(conn, "250 OK\r\n")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			f.mu.Lock()
			f.rcpts = append(f.rcpts, smtpPathArg(line))
			f.mu.Unlock()
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

func (f *fakeSMTPServer) snapshot() (string, []string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.from, append([]string{}, f.rcpts...), f.data
}

// smtpPathArg extrahiert die Adresse aus "MAIL FROM:<a@b> PARAM=X".
func smtpPathArg(line string) string {
	if i, j := strings.Index(line, "<"), strings.Index(line, ">"); i >= 0 && j > i {
		return line[i+1 : j]
	}
	return strings.TrimSpace(line)
}
