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
		t.Fatal("a valid signature must be accepted")
	}
	if VerifySignature(secret, []byte(`{"ticket":{"id":43}}`), header) {
		t.Fatal("a tampered body must be rejected")
	}
	if VerifySignature(secret, body, "sha1=deadbeef") {
		t.Fatal("a wrong signature must be rejected")
	}
	if VerifySignature(secret, body, "") {
		t.Fatal("a missing header must be rejected")
	}
	if !VerifySignature("", body, "") {
		t.Fatal("an empty secret disables the check (dev mode)")
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
		t.Fatalf("payload parsed wrongly: %+v", p)
	}
	if !p.IsCustomerMessage() {
		t.Fatal("a customer article must be recognized as a customer message")
	}
	if CorrelationKey(p.Ticket.ID) != "zammad:ticket:42" {
		t.Fatalf("correlation key: %s", CorrelationKey(p.Ticket.ID))
	}
}

func TestParseWebhookRejectsMissingTicket(t *testing.T) {
	if _, err := ParseWebhook([]byte(`{"article":{"id":1}}`)); err == nil {
		t.Fatal("a payload without ticket.id must be rejected")
	}
}

func TestAgentArticleTriggersNoWake(t *testing.T) {
	p := WebhookPayload{}
	p.Article.Sender = "Agent"
	if p.IsCustomerMessage() {
		t.Fatal("an agent article must not trigger a wake (echo loop)")
	}
	p.Article.Sender = "Customer"
	p.Article.Internal = true
	if p.IsCustomerMessage() {
		t.Fatal("internal articles must not trigger a wake")
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
		t.Fatalf("token auth header wrong: %q", gotAuth)
	}

	arts, err := c.ListArticles(ctx, 42)
	if err != nil || len(arts) != 1 {
		t.Fatalf("ListArticles: %v %+v", err, arts)
	}

	if _, err := c.Reply(ctx, 42, "Bitte Screenshot schicken", false); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if gotBody["internal"] != false || gotBody["ticket_id"] != float64(42) {
		t.Fatalf("reply body wrong: %+v", gotBody)
	}

	if err := c.SetState(ctx, 42, "pending reminder"); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/v1/tickets/42" {
		t.Fatalf("SetState must be PUT /tickets/42: %s %s", gotMethod, gotPath)
	}
	if gotBody["state"] != "pending reminder" || gotBody["pending_time"] == nil {
		t.Fatalf("a pending state needs pending_time: %+v", gotBody)
	}
}

func TestClientErrorSurface(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"Not authorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "falsch")
	if _, err := c.GetTicket(context.Background(), 1); err == nil {
		t.Fatal("an HTTP error must surface as an error")
	}
}
