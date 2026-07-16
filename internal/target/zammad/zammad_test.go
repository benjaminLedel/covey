package zammad

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerifySignature(t *testing.T) {
	secret := "webhook-geheim"
	body := []byte(`{"ticket":{"id":42}}`)
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write(body)
	header := "sha1=" + hex.EncodeToString(mac.Sum(nil))

	if !VerifySignature(secret, body, header) {
		t.Fatal("gültige Signatur muss akzeptiert werden")
	}
	if VerifySignature(secret, []byte(`{"ticket":{"id":43}}`), header) {
		t.Fatal("manipulierter Body muss abgelehnt werden")
	}
	if VerifySignature(secret, body, "sha1=deadbeef") {
		t.Fatal("falsche Signatur muss abgelehnt werden")
	}
	if VerifySignature(secret, body, "") {
		t.Fatal("fehlender Header muss abgelehnt werden")
	}
	if !VerifySignature("", body, "") {
		t.Fatal("leeres Secret deaktiviert die Prüfung (Dev-Modus)")
	}
}

func TestParseWebhook(t *testing.T) {
	body := []byte(`{"ticket":{"id":42,"number":"20001","title":"Login kaputt","state":"open","article_ids":[1,2]},
		"article":{"id":2,"sender":"Customer","body":"Es geht wieder nicht","internal":false}}`)
	p, err := ParseWebhook(body)
	if err != nil {
		t.Fatal(err)
	}
	if p.Ticket.ID != 42 || p.Article.Sender != "Customer" {
		t.Fatalf("payload falsch geparst: %+v", p)
	}
	if !p.IsCustomerMessage() {
		t.Fatal("Kunden-Artikel muss als Customer-Message erkannt werden")
	}
	if CorrelationKey(p.Ticket.ID) != "zammad:ticket:42" {
		t.Fatalf("korrelations-key: %s", CorrelationKey(p.Ticket.ID))
	}
}

func TestParseWebhookRejectsMissingTicket(t *testing.T) {
	if _, err := ParseWebhook([]byte(`{"article":{"id":1}}`)); err == nil {
		t.Fatal("payload ohne ticket.id muss abgelehnt werden")
	}
}

func TestAgentArticleTriggersNoWake(t *testing.T) {
	p := WebhookPayload{}
	p.Article.Sender = "Agent"
	if p.IsCustomerMessage() {
		t.Fatal("Agent-Artikel darf keinen Wake auslösen (Echo-Schleife)")
	}
	p.Article.Sender = "Customer"
	p.Article.Internal = true
	if p.IsCustomerMessage() {
		t.Fatal("interne Artikel dürfen keinen Wake auslösen")
	}
}

func TestClientActions(t *testing.T) {
	var gotAuth, gotPath, gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotBody = nil
		if r.Body != nil {
			json.NewDecoder(r.Body).Decode(&gotBody)
		}
		switch {
		case r.URL.Path == "/api/v1/tickets/42" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(Ticket{ID: 42, Title: "Login kaputt", State: "open"})
		case r.URL.Path == "/api/v1/ticket_articles/by_ticket/42":
			json.NewEncoder(w).Encode([]Article{{ID: 1, Body: "Hilfe"}})
		case r.URL.Path == "/api/v1/ticket_articles" && r.Method == http.MethodPost:
			json.NewEncoder(w).Encode(Article{ID: 2})
		default:
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-token")
	ctx := context.Background()

	tk, err := c.GetTicket(ctx, 42)
	if err != nil || tk.Title != "Login kaputt" {
		t.Fatalf("GetTicket: %v %+v", err, tk)
	}
	if gotAuth != "Token token=test-token" {
		t.Fatalf("Token-Auth-Header falsch: %q", gotAuth)
	}

	arts, err := c.ListArticles(ctx, 42)
	if err != nil || len(arts) != 1 {
		t.Fatalf("ListArticles: %v %+v", err, arts)
	}

	if _, err := c.Reply(ctx, 42, "Bitte Screenshot schicken", false); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if gotBody["internal"] != false || gotBody["ticket_id"] != float64(42) {
		t.Fatalf("Reply-Body falsch: %+v", gotBody)
	}

	if err := c.SetState(ctx, 42, "pending reminder"); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/v1/tickets/42" {
		t.Fatalf("SetState muss PUT /tickets/42 sein: %s %s", gotMethod, gotPath)
	}
	if gotBody["state"] != "pending reminder" || gotBody["pending_time"] == nil {
		t.Fatalf("pending-State braucht pending_time: %+v", gotBody)
	}
}

func TestClientErrorSurface(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"Not authorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "falsch")
	if _, err := c.GetTicket(context.Background(), 1); err == nil {
		t.Fatal("HTTP-Fehler muss als error auftauchen")
	}
}
