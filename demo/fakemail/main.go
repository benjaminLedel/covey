// fakemail is a minimal mail double for local demos of the email plugin: an
// in-memory IMAP server (go-imap), an SMTP entry point that delivers to the
// local mailboxes, and a small HTTP API for injecting and reading along.
// Every delivery is logged. Start: go run ./demo/fakemail
//
//	IMAP :1143 — plaintext, matching the email_url scheme imap+insecure
//	SMTP :1025 — plaintext, matching smtp+insecure
//	HTTP :8025 — POST /send {from,to,subject,body} injects a mail,
//	             GET /mails lists all deliveries (newest last)
//
// Mailboxes: agent@covey.demo (password agent-pw) for the Covey agent,
// kunde@covey.demo (kunde-pw) as the counterpart. Secrets for the agent:
//
//	email_url   = imap+insecure://127.0.0.1:1143 smtp+insecure://127.0.0.1:1025
//	email_token = agent@covey.demo:agent-pw
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// accounts are the fixed demo mailboxes.
var accounts = map[string]string{
	"agent@covey.demo": "agent-pw",
	"kunde@covey.demo": "kunde-pw",
}

func main() {
	imapAddr := flag.String("imap", ":1143", "IMAP address (plaintext)")
	smtpAddr := flag.String("smtp", ":1025", "SMTP address (plaintext)")
	httpAddr := flag.String("http", ":8025", "HTTP address (inject/log)")
	flag.Parse()

	st, err := start(*imapAddr, *smtpAddr, *httpAddr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("fake-mail: imap %s, smtp %s, http %s", st.imapLn.Addr(), st.smtpLn.Addr(), st.httpLn.Addr())
	for user := range accounts {
		log.Printf("mailbox %s (password %s)", user, accounts[user])
	}
	select {}
}

// store holds the mailboxes and the delivery log.
type store struct {
	mu     sync.Mutex
	users  map[string]*imapmemserver.User
	log    []delivered
	imapLn net.Listener
	smtpLn net.Listener
	httpLn net.Listener
}

// delivered is one entry in the delivery log (GET /mails).
type delivered struct {
	Time    time.Time `json:"time"`
	Via     string    `json:"via"` // "smtp" (real send) or "http" (inject)
	From    string    `json:"from"`
	To      []string  `json:"to"`
	Subject string    `json:"subject"`
	Body    string    `json:"body"`
}

// start brings all three servers up (":0" = free port, for tests).
func start(imapAddr, smtpAddr, httpAddr string) (*store, error) {
	st := &store{users: map[string]*imapmemserver.User{}}
	mem := imapmemserver.New()
	for user, pass := range accounts {
		u := imapmemserver.NewUser(user, pass)
		if err := u.Create("INBOX", nil); err != nil {
			return nil, err
		}
		mem.AddUser(u)
		st.users[strings.ToLower(user)] = u
	}

	var err error
	if st.imapLn, err = net.Listen("tcp", imapAddr); err != nil {
		return nil, err
	}
	imapSrv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
	})
	go imapSrv.Serve(st.imapLn)

	if st.smtpLn, err = net.Listen("tcp", smtpAddr); err != nil {
		return nil, err
	}
	go st.serveSMTP()

	if st.httpLn, err = net.Listen("tcp", httpAddr); err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /send", st.handleSend)
	mux.HandleFunc("GET /mails", st.handleMails)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 20 * time.Second}
	go func() { _ = srv.Serve(st.httpLn) }()
	return st, nil
}

// deliver puts a raw mail into the INBOX of every known recipient and records
// it. Unknown recipients are logged and dropped — just like a real mail server
// doing local delivery.
func (st *store) deliver(via, from string, rcpts []string, raw []byte) {
	entry := delivered{Time: time.Now(), Via: via, From: from, To: rcpts}
	entry.Subject, entry.Body = parseForLog(raw)
	for _, rcpt := range rcpts {
		u, ok := st.users[strings.ToLower(strings.TrimSpace(rcpt))]
		if !ok {
			log.Printf("→ %s: recipient %q unknown, dropped", via, rcpt)
			continue
		}
		_, err := u.Append("INBOX", literal{bytes.NewReader(raw)}, &imap.AppendOptions{Time: time.Now()})
		if err != nil {
			log.Printf("→ %s: delivery to %s failed: %v", via, rcpt, err)
		}
	}
	st.mu.Lock()
	st.log = append(st.log, entry)
	st.mu.Unlock()
	log.Printf("→ %s: %s → %v subject=%q", via, from, rcpts, entry.Subject)
}

// handleSend injects a mail over HTTP (demo convenience: the "customer" needs
// no mail client).
func (st *store) handleSend(w http.ResponseWriter, r *http.Request) {
	var in struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		Body    string   `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.From == "" || len(in.To) == 0 {
		http.Error(w, "expected {from,to[],subject,body}", http.StatusBadRequest)
		return
	}
	raw := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%d@fakemail>\r\n\r\n%s\r\n",
		in.From, strings.Join(in.To, ", "), in.Subject,
		time.Now().Format(time.RFC1123Z), time.Now().UnixNano(), in.Body)
	st.deliver("http", in.From, in.To, []byte(raw))
	json.NewEncoder(w).Encode(map[string]any{"delivered_to": in.To})
}

func (st *store) handleMails(w http.ResponseWriter, _ *http.Request) {
	st.mu.Lock()
	defer st.mu.Unlock()
	json.NewEncoder(w).Encode(st.log)
}

// serveSMTP accepts plaintext SMTP submissions (EHLO/MAIL/RCPT/DATA) and
// delivers them locally — the return channel for the agent's replies.
func (st *store) serveSMTP() {
	for {
		conn, err := st.smtpLn.Accept()
		if err != nil {
			return
		}
		go st.handleSMTP(conn)
	}
}

func (st *store) handleSMTP(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	fmt.Fprintf(conn, "220 fakemail ESMTP\r\n")
	var from string
	var rcpts []string
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
				st.deliver("smtp", from, rcpts, data.Bytes())
				from, rcpts, data = "", nil, bytes.Buffer{}
				fmt.Fprintf(conn, "250 OK\r\n")
			} else {
				data.WriteString(line)
			}
			continue
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			fmt.Fprintf(conn, "250-fakemail\r\n250 8BITMIME\r\n")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			from = pathArg(line)
			fmt.Fprintf(conn, "250 OK\r\n")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			rcpts = append(rcpts, pathArg(line))
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

// pathArg extracts the address from "MAIL FROM:<a@b> PARAM" / "RCPT TO:<a@b>".
func pathArg(line string) string {
	if i, j := strings.Index(line, "<"), strings.Index(line, ">"); i >= 0 && j > i {
		return line[i+1 : j]
	}
	return strings.TrimSpace(line)
}

// parseForLog pulls subject and the start of the text out of the raw mail for
// the delivery log (best effort — the log is display, not truth).
func parseForLog(raw []byte) (subject, body string) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return "", ""
	}
	dec := mime.WordDecoder{}
	subject, _ = dec.DecodeHeader(msg.Header.Get("Subject"))
	var r io.Reader = msg.Body
	if strings.EqualFold(msg.Header.Get("Content-Transfer-Encoding"), "quoted-printable") {
		r = quotedprintable.NewReader(msg.Body)
	}
	b, _ := io.ReadAll(io.LimitReader(r, 2000))
	return subject, strings.TrimSpace(string(b))
}

// literal turns a bytes.Reader into an imap.LiteralReader.
type literal struct{ *bytes.Reader }

func (l literal) Size() int64 { return int64(l.Reader.Len()) }
