package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/benjaminLedel/covey-plugin-sdk/target"
	"github.com/benjaminLedel/covey-plugin-pack/email"
)

// TestRoundtripWithEmailPlugin keeps the double honest: the complete demo
// cycle with the real email plugin — the customer feeds in over HTTP,
// the agent reads by IMAP and answers by SMTP, and the answer lands in the
// customer mailbox and in the delivery log.
func TestRoundtripWithEmailPlugin(t *testing.T) {
	st, err := start("127.0.0.1:0", "127.0.0.1:0", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.imapLn.Close(); st.smtpLn.Close(); st.httpLn.Close() })

	cred := func(user, pass string) target.Credential {
		return target.Credential{
			BaseURL: fmt.Sprintf("imap+insecure://%s smtp+insecure://%s", st.imapLn.Addr(), st.smtpLn.Addr()),
			Token:   user + ":" + pass,
		}
	}
	run := func(c target.Credential, action, params string) any {
		t.Helper()
		res, err := (email.System{}).Execute(context.Background(), action, json.RawMessage(params), c)
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		return res
	}

	// Kunde speist eine Anfrage per HTTP ein.
	body, _ := json.Marshal(map[string]any{
		"from": "kunde@covey.demo", "to": []string{"agent@covey.demo"},
		"subject": "VPN geht nicht", "body": "Seit heute früh keine Verbindung.",
	})
	resp, err := http.Post("http://"+st.httpLn.Addr().String()+"/send", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// The agent: read and answer.
	agentCred := cred("agent@covey.demo", "agent-pw")
	unread := run(agentCred, "list_unread", `{}`).([]email.MessageSummary)
	if len(unread) != 1 || unread[0].From != "kunde@covey.demo" {
		t.Fatalf("agent-inbox: %+v", unread)
	}
	run(agentCred, "reply", fmt.Sprintf(`{"uid":%d,"body":"Wir schauen sofort drauf."}`, unread[0].UID))

	// Die Antwort liegt im Kunden-Postfach …
	kundeCred := cred("kunde@covey.demo", "kunde-pw")
	kundeInbox := run(kundeCred, "list_unread", `{}`).([]email.MessageSummary)
	if len(kundeInbox) != 1 || kundeInbox[0].Subject != "Re: VPN geht nicht" {
		t.Fatalf("kunden-inbox: %+v", kundeInbox)
	}
	msg := run(kundeCred, "get_message", fmt.Sprintf(`{"uid":%d}`, kundeInbox[0].UID)).(*email.Message)
	if !strings.Contains(msg.Body, "Wir schauen sofort drauf.") {
		t.Fatalf("antwort-text: %+v", msg)
	}

	// … and in the delivery log (the HTTP inject + the SMTP answer).
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.log) != 2 || st.log[0].Via != "http" || st.log[1].Via != "smtp" ||
		!strings.Contains(st.log[1].Body, "Wir schauen sofort drauf.") {
		t.Fatalf("zustell-log: %+v", st.log)
	}
}
