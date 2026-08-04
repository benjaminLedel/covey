package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
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

	"covey/internal/backlog"
)

// TestEmailPlugin: the e-mail target system end-to-end through the stack — the
// broker's two-secret convention (email_url encodes the IMAP AND the SMTP
// endpoint), the ACCESS.md gate and the action proxy right through to a real
// protocol roundtrip (in-memory IMAP, fake SMTP).
func TestEmailPlugin(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	const mailUser = "agent@example.com"
	imapAddr := startMemIMAP(t, mailUser, "pw")
	appendTestMail(t, imapAddr, mailUser, "pw",
		"From: Kunde <kunde@example.com>\r\nTo: agent@example.com\r\nSubject: Drucker kaputt\r\nMessage-ID: <t1@example.com>\r\n\r\nDer Drucker druckt nicht.\r\n")
	smtp := startFakeSMTP(t)

	// Activation is opt-in — the test org enables the built-in.
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
	waitFor(t, "mail task done", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	// The reply went out as a real SMTP delivery — with an envelope and the
	// threading headers from the original mail.
	from, rcpts, data := smtp.snapshot()
	if from != mailUser || strings.Join(rcpts, ",") != "kunde@example.com" {
		t.Fatalf("smtp envelope: %q → %v", from, rcpts)
	}
	if !strings.Contains(data, "Subject: Re: Drucker kaputt") ||
		!strings.Contains(data, "In-Reply-To: <t1@example.com>") {
		t.Fatalf("reply message incomplete:\n%s", data)
	}
}

// startMemIMAP starts an in-memory IMAP server (plaintext — matching the
// plugin's imap+insecure test configuration).
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

// appendTestMail puts a raw mail into the mailbox via IMAP APPEND (UID 1, 2, …).
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

// fakeSMTPServer accepts a delivery in plaintext and records envelope +
// message — the SMTP counterpart to fakeZammad.
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

// smtpPathArg extracts the address from "MAIL FROM:<a@b> PARAM=X".
func smtpPathArg(line string) string {
	if i, j := strings.Index(line, "<"), strings.Index(line, ">"); i >= 0 && j > i {
		return line[i+1 : j]
	}
	return strings.TrimSpace(line)
}

// mailMitAnhang builds a multipart/mixed mail with exactly one attachment.
func mailMitAnhang(messageID, betreff, fileName, contentType, inhalt string) string {
	return "From: Kunde <kunde@example.com>\r\n" +
		"To: agent@example.com\r\n" +
		"Subject: " + betreff + "\r\n" +
		"Message-ID: <" + messageID + ">\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"grenze\"\r\n\r\n" +
		"--grenze\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
		"Anbei.\r\n" +
		"--grenze\r\n" +
		"Content-Type: " + contentType + "; name=\"" + fileName + "\"\r\n" +
		"Content-Disposition: attachment; filename=\"" + fileName + "\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		base64.StdEncoding.EncodeToString([]byte(inhalt)) + "\r\n" +
		"--grenze--\r\n"
}

// TestEmailGetAttachment: the vertical slice for get_attachment — from the IMAP
// server via the action proxy to the bytes in the sandbox. Analogous to the
// Teams counterpart in teams_test.go (GitHub #2, point 7).
//
// The second part is the actual reason for the test: two mails each carrying
// `rechnung.pdf` used to overwrite each other silently, and an agent that had
// memorized the path then read the wrong document (point 1). A unit test of the
// helper shows the name assignment; only here is it established that it also
// takes hold on the real path.
func TestEmailGetAttachment(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	const mailUser = "agent@example.com"
	imapAddr := startMemIMAP(t, mailUser, "pw")
	appendTestMail(t, imapAddr, mailUser, "pw",
		mailMitAnhang("a1@example.com", "Rechnung Januar", "rechnung.pdf", "application/pdf", "%PDF-1.4 januar"))
	appendTestMail(t, imapAddr, mailUser, "pw",
		mailMitAnhang("a2@example.com", "Rechnung Februar", "rechnung.pdf", "application/pdf", "%PDF-1.4 februar"))
	smtp := startFakeSMTP(t)

	if _, err := s.pool.Exec(ctx, `INSERT INTO target_plugins (org_id, name, kind, enabled)
		VALUES ($1,'email','builtin',TRUE)`, s.orgID); err != nil {
		t.Fatal(err)
	}

	agent, err := s.registry.Create(ctx, s.orgID, "anhang-leser", "Anhang-Agent", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md":   "# Anhang-Agent",
		"ACCESS.md": "- system: email scope: read,write",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	s.secrets.Put(ctx, s.orgID, "email_url",
		fmt.Sprintf("imap+insecure://%s smtp+insecure://%s", imapAddr, smtp.addr()))
	s.secrets.Put(ctx, s.orgID, "email_token", mailUser+":pw")
	s.secrets.Assign(ctx, s.orgID, "email_url", agent.ID)
	s.secrets.Assign(ctx, s.orgID, "email_token", agent.ID)

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Rechnungen holen",
		`[mock:action email/get_attachment {"uid":1,"name":"rechnung.pdf"}] `+
			`[mock:action email/get_attachment {"uid":2,"name":"rechnung.pdf"}] `+
			`[mock:result Anhänge geholt]`,
		"manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "attachment task done", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	dir := filepath.Join(s.homeBase, agent.ID.String(), "attachments")

	// First attachment: under its own name, with the real bytes.
	first, err := os.ReadFile(filepath.Join(dir, "rechnung.pdf"))
	if err != nil || !strings.Contains(string(first), "januar") {
		t.Fatalf("first attachment not materialized: %q (err=%v)", first, err)
	}

	// Second attachment with the same name: its own file — and the first one is untouched.
	second, err := os.ReadFile(filepath.Join(dir, "rechnung-2.pdf"))
	if err != nil || !strings.Contains(string(second), "februar") {
		t.Fatalf("second attachment not stored collision-free: %q (err=%v)", second, err)
	}
	if !strings.Contains(string(first), "januar") {
		t.Fatal("the second attachment overwrote the first")
	}
}
