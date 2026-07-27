package teams

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"covey/internal/target"

	"github.com/golang-jwt/jwt/v5"
)

func TestParseWebhookAndKeys(t *testing.T) {
	body := []byte(`{"type":"message","id":"a1","text":"<at>Bot</at> Hallo Welt",
		"serviceUrl":"https://smba.example/emea/","channelId":"msteams",
		"from":{"id":"29:user","name":"Alice"},"recipient":{"id":"28:bot","name":"Covey"},
		"conversation":{"id":"19:conv1","conversationType":"personal","tenantId":"t1"}}`)
	a, err := ParseWebhook(body)
	if err != nil {
		t.Fatal(err)
	}
	if a.CleanText() != "Hallo Welt" {
		t.Fatalf("mention nicht entfernt: %q", a.CleanText())
	}
	if a.DedupKey() != "teams:activity:a1" {
		t.Fatalf("dedup-key: %s", a.DedupKey())
	}
	if CorrelationKey(a.Conversation.ID) != "teams:conversation:19:conv1" {
		t.Fatalf("korrelations-key: %s", CorrelationKey(a.Conversation.ID))
	}
	if !a.ShouldWake() {
		t.Fatal("Nutzer-Nachricht muss wecken")
	}
}

func TestParseWebhookRejectsTypeless(t *testing.T) {
	if _, err := ParseWebhook([]byte(`{"id":"x"}`)); err == nil {
		t.Fatal("Activity ohne type muss abgelehnt werden")
	}
}

func TestShouldWakeFilters(t *testing.T) {
	base := func() Activity {
		return Activity{
			Type: "message", Text: "hi",
			From:         ChannelAccount{ID: "29:user"},
			Recipient:    ChannelAccount{ID: "28:bot"},
			Conversation: ConversationAccount{ID: "19:c"},
		}
	}

	if a := base(); !a.ShouldWake() {
		t.Fatal("Basisfall muss wecken")
	}

	// Echo der eigenen Bot-Antwort: from == recipient.
	echo := base()
	echo.From.ID = "28:bot"
	if echo.ShouldWake() {
		t.Fatal("Echo (from==recipient) darf nicht wecken")
	}
	if !echo.IsEcho() {
		t.Fatal("Echo muss als solches erkannt werden")
	}

	// Nicht-message-Activity (z. B. conversationUpdate).
	upd := base()
	upd.Type = "conversationUpdate"
	if upd.ShouldWake() {
		t.Fatal("conversationUpdate darf nicht wecken")
	}

	// Leerer Text (nur Mention).
	empty := base()
	empty.Text = "<at>Bot</at>"
	if empty.ShouldWake() {
		t.Fatal("leerer Text darf nicht wecken")
	}

	// Tenant-Filter.
	t.Setenv("COVEY_TEAMS_INTAKE_TENANTS", "erlaubt-tenant")
	scoped := base()
	scoped.Conversation.TenantID = "anderer-tenant"
	if scoped.ShouldWake() {
		t.Fatal("Nachricht aus fremdem Tenant darf nicht wecken")
	}
	scoped.Conversation.TenantID = "erlaubt-tenant"
	if !scoped.ShouldWake() {
		t.Fatal("Nachricht aus erlaubtem Tenant muss wecken")
	}
}

func TestParseCredential(t *testing.T) {
	id, pass, err := parseCredential("app-guid:secret:with:colons")
	if err != nil || id != "app-guid" || pass != "secret:with:colons" {
		t.Fatalf("parseCredential: %q %q %v", id, pass, err)
	}
	if _, _, err := parseCredential("nurid"); err == nil {
		t.Fatal("Credential ohne ':' muss Fehler sein")
	}
	if _, _, err := parseCredential(":passonly"); err == nil {
		t.Fatal("Credential ohne appId muss Fehler sein")
	}
}

func TestJWKToRSARoundtrip(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	n := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())
	pub, err := jwkToRSA(n, e)
	if err != nil {
		t.Fatal(err)
	}
	if pub.N.Cmp(key.N) != 0 || pub.E != key.E {
		t.Fatal("JWK→RSA-Roundtrip stimmt nicht")
	}
}

func TestVerifyToken(t *testing.T) {
	if !VerifyToken("", "irgendwas") {
		t.Fatal("leere appID = Verifikation aus (Dev)")
	}
	if VerifyToken("bot-app", "kein-bearer") {
		t.Fatal("Header ohne Bearer muss abgelehnt werden")
	}

	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	sign := func(claims jwt.MapClaims) string {
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = "test-kid"
		s, err := tok.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	// Verifier mit injiziertem Schlüssel (kein Netz-Abruf).
	defaultVerifier.keyFunc = func(*jwt.Token) (any, error) { return &key.PublicKey, nil }
	t.Cleanup(func() { defaultVerifier.keyFunc = nil })

	good := sign(jwt.MapClaims{
		"iss": botFrameworkIssuer,
		"aud": "bot-app",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if !VerifyToken("bot-app", "Bearer "+good) {
		t.Fatal("gültiges Token muss akzeptiert werden")
	}

	wrongAud := sign(jwt.MapClaims{
		"iss": botFrameworkIssuer,
		"aud": "anderer-bot",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if VerifyToken("bot-app", "Bearer "+wrongAud) {
		t.Fatal("falsche Audience muss abgelehnt werden")
	}

	expired := sign(jwt.MapClaims{
		"iss": botFrameworkIssuer,
		"aud": "bot-app",
		"exp": time.Now().Add(-time.Hour).Unix(),
	})
	if VerifyToken("bot-app", "Bearer "+expired) {
		t.Fatal("abgelaufenes Token muss abgelehnt werden")
	}
}

func TestActionSubject(t *testing.T) {
	for action, want := range map[string]string{
		"send": "teams:send", "reply": "teams:reply", "create_conversation": "teams:create_conversation",
	} {
		if got := (System{}).ActionSubject(action, nil); got != want {
			t.Fatalf("ActionSubject(%s)=%s, want %s", action, got, want)
		}
	}
}

// fakeConnector spielt Token-Endpoint + Bot-Connector in einem Server.
func fakeConnector(t *testing.T, record *[]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"access_token": "connector-token", "expires_in": 3600})
	})
	mux.HandleFunc("POST /v3/conversations/{cid}/activities", func(w http.ResponseWriter, r *http.Request) {
		*record = append(*record, "send "+r.PathValue("cid")+" auth="+r.Header.Get("Authorization"))
		json.NewEncoder(w).Encode(ResourceResponse{ID: "msg-1"})
	})
	mux.HandleFunc("POST /v3/conversations/{cid}/activities/{aid}", func(w http.ResponseWriter, r *http.Request) {
		*record = append(*record, "reply "+r.PathValue("cid")+"/"+r.PathValue("aid"))
		json.NewEncoder(w).Encode(ResourceResponse{ID: "msg-2"})
	})
	mux.HandleFunc("POST /v3/conversations", func(w http.ResponseWriter, r *http.Request) {
		*record = append(*record, "create-conv")
		json.NewEncoder(w).Encode(ResourceResponse{ID: "19:new"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestExecuteActions(t *testing.T) {
	var rec []string
	srv := fakeConnector(t, &rec)
	cred := target.Credential{BaseURL: srv.URL + "/token", Token: "app-id:app-secret"}
	sys := System{}
	ctx := context.Background()

	// send
	out, err := sys.Execute(ctx, "send", json.RawMessage(
		`{"service_url":"`+srv.URL+`","conversation_id":"19:c","text":"hallo"}`), cred)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if rr, ok := out.(ResourceResponse); !ok || rr.ID != "msg-1" {
		t.Fatalf("send-Antwort: %#v", out)
	}
	if len(rec) == 0 || rec[0] != "send 19:c auth=Bearer connector-token" {
		t.Fatalf("send-Request falsch: %v", rec)
	}

	// reply mit activity-id
	if _, err := sys.Execute(ctx, "reply", json.RawMessage(
		`{"service_url":"`+srv.URL+`","conversation_id":"19:c","reply_to_activity_id":"a9","text":"hi"}`), cred); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if rec[len(rec)-1] != "reply 19:c/a9" {
		t.Fatalf("reply-Request falsch: %v", rec)
	}

	// reply ohne activity-id fällt auf send zurück
	if _, err := sys.Execute(ctx, "reply", json.RawMessage(
		`{"service_url":"`+srv.URL+`","conversation_id":"19:c","text":"hi"}`), cred); err != nil {
		t.Fatalf("reply-fallback: %v", err)
	}
	if rec[len(rec)-1] != "send 19:c auth=Bearer connector-token" {
		t.Fatalf("reply ohne activity-id muss senden: %v", rec)
	}

	// create_conversation (POST /conversations + anschließendes send)
	if _, err := sys.Execute(ctx, "create_conversation", json.RawMessage(
		`{"service_url":"`+srv.URL+`","tenant_id":"t1","user_id":"29:u","text":"servus"}`), cred); err != nil {
		t.Fatalf("create_conversation: %v", err)
	}
	if rec[len(rec)-2] != "create-conv" || rec[len(rec)-1] != "send 19:new auth=Bearer connector-token" {
		t.Fatalf("create_conversation-Ablauf falsch: %v", rec)
	}
}

func TestExecuteValidation(t *testing.T) {
	sys := System{}
	cred := target.Credential{Token: "app:secret"}
	if _, err := sys.Execute(context.Background(), "send",
		json.RawMessage(`{"conversation_id":"c","text":"x"}`), cred); err == nil {
		t.Fatal("send ohne service_url muss Fehler sein")
	}
	if _, err := sys.Execute(context.Background(), "unbekannt",
		json.RawMessage(`{}`), cred); err == nil {
		t.Fatal("unbekannte Aktion muss Fehler sein")
	}
	if _, err := sys.Execute(context.Background(), "send",
		json.RawMessage(`{}`), target.Credential{Token: "kaputt"}); err == nil {
		t.Fatal("kaputtes Credential muss Fehler sein")
	}
}
