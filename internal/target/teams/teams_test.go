package teams

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/benjaminLedel/covey-plugin-sdk/target"

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
		t.Fatalf("mention not removed: %q", a.CleanText())
	}
	if a.DedupKey() != "teams:activity:a1" {
		t.Fatalf("dedup key: %s", a.DedupKey())
	}
	if CorrelationKey(a.Conversation.ID) != "teams:conversation:19:conv1" {
		t.Fatalf("correlation key: %s", CorrelationKey(a.Conversation.ID))
	}
	if !a.ShouldWake() {
		t.Fatal("a user message must wake")
	}
}

func TestParseWebhookRejectsTypeless(t *testing.T) {
	if _, err := ParseWebhook([]byte(`{"id":"x"}`)); err == nil {
		t.Fatal("an activity without a type must be rejected")
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
		t.Fatal("the base case must wake")
	}

	// Echo of the bot's own answer: from == recipient.
	echo := base()
	echo.From.ID = "28:bot"
	if echo.ShouldWake() {
		t.Fatal("an echo (from==recipient) must not wake")
	}
	if !echo.IsEcho() {
		t.Fatal("an echo must be recognized as such")
	}

	// Non-message activity (e.g. conversationUpdate).
	upd := base()
	upd.Type = "conversationUpdate"
	if upd.ShouldWake() {
		t.Fatal("conversationUpdate must not wake")
	}

	// Empty text (mention only).
	empty := base()
	empty.Text = "<at>Bot</at>"
	if empty.ShouldWake() {
		t.Fatal("empty text must not wake")
	}

	// Tenant filter.
	t.Setenv("COVEY_TEAMS_INTAKE_TENANTS", "erlaubt-tenant")
	scoped := base()
	scoped.Conversation.TenantID = "anderer-tenant"
	if scoped.ShouldWake() {
		t.Fatal("a message from a foreign tenant must not wake")
	}
	scoped.Conversation.TenantID = "erlaubt-tenant"
	if !scoped.ShouldWake() {
		t.Fatal("a message from an allowed tenant must wake")
	}
}

func TestParseCredential(t *testing.T) {
	id, pass, err := parseCredential("app-guid:secret:with:colons")
	if err != nil || id != "app-guid" || pass != "secret:with:colons" {
		t.Fatalf("parseCredential: %q %q %v", id, pass, err)
	}
	if _, _, err := parseCredential("nurid"); err == nil {
		t.Fatal("a credential without ':' must be an error")
	}
	if _, _, err := parseCredential(":passonly"); err == nil {
		t.Fatal("a credential without an appId must be an error")
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
		t.Fatal("the JWK→RSA roundtrip does not match")
	}
}

func TestVerifyToken(t *testing.T) {
	if !VerifyToken("", "irgendwas") {
		t.Fatal("empty appID = verification off (dev)")
	}
	if VerifyToken("bot-app", "kein-bearer") {
		t.Fatal("a header without Bearer must be rejected")
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
	// Verifier with an injected key (no network fetch).
	defaultVerifier.keyFunc = func(*jwt.Token) (any, error) { return &key.PublicKey, nil }
	t.Cleanup(func() { defaultVerifier.keyFunc = nil })

	good := sign(jwt.MapClaims{
		"iss": botFrameworkIssuer,
		"aud": "bot-app",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if !VerifyToken("bot-app", "Bearer "+good) {
		t.Fatal("a valid token must be accepted")
	}

	wrongAud := sign(jwt.MapClaims{
		"iss": botFrameworkIssuer,
		"aud": "anderer-bot",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if VerifyToken("bot-app", "Bearer "+wrongAud) {
		t.Fatal("a wrong audience must be rejected")
	}

	expired := sign(jwt.MapClaims{
		"iss": botFrameworkIssuer,
		"aud": "bot-app",
		"exp": time.Now().Add(-time.Hour).Unix(),
	})
	if VerifyToken("bot-app", "Bearer "+expired) {
		t.Fatal("an expired token must be rejected")
	}
}

func TestAttachmentsParsing(t *testing.T) {
	body := []byte(`{"type":"message","id":"a2","text":"",
		"from":{"id":"29:user"},"recipient":{"id":"28:bot"},
		"conversation":{"id":"19:c"},
		"attachments":[
			{"contentType":"text/html","content":"<p>msg</p>"},
			{"contentType":"application/vnd.microsoft.teams.file.download.info","name":"report.pdf",
			 "content":{"downloadUrl":"https://share.example/dl/report.pdf","fileType":"pdf"}},
			{"contentType":"image/png","name":"bild.png","contentUrl":"https://smba.example/v3/attachments/x"}
		]}`)
	a, err := ParseWebhook(body)
	if err != nil {
		t.Fatal(err)
	}
	files := a.Files()
	if len(files) != 2 {
		t.Fatalf("expected 2 file attachments (text/html filtered out), got %d: %+v", len(files), files)
	}
	if files[0].DownloadURL() != "https://share.example/dl/report.pdf" || files[0].Filename() != "report.pdf" {
		t.Fatalf("file.download.info wrong: %+v", files[0])
	}
	if files[1].DownloadURL() != "https://smba.example/v3/attachments/x" {
		t.Fatalf("inline image URL wrong: %+v", files[1])
	}
	// A message without text but with an attachment must wake.
	if !a.ShouldWake() {
		t.Fatal("a message with an attachment only must wake")
	}
}

func TestDownloadAttachmentToSandbox(t *testing.T) {
	// Server: /preauth delivers directly (without a token), /connector demands Bearer.
	var sawToken bool
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"access_token": "connector-token", "expires_in": 3600})
	})
	mux.HandleFunc("GET /preauth", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-1.4 fake"))
	})
	mux.HandleFunc("GET /connector", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer connector-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		sawToken = true
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("PNGDATA"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := NewClient(target.Credential{BaseURL: srv.URL + "/token", Token: "app:secret"})
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	ctx := context.Background()

	// Pre-authorized URL (no token needed).
	res, err := DownloadAttachmentToSandbox(ctx, c, srv.URL+"/preauth", "report.pdf", work)
	if err != nil {
		t.Fatalf("preauth download: %v", err)
	}
	if res.Filename != "report.pdf" || res.ContentType != "application/pdf" || res.Bytes == 0 {
		t.Fatalf("preauth result wrong: %+v", res)
	}
	if data, _ := os.ReadFile(res.Path); string(data) != "%PDF-1.4 fake" {
		t.Fatalf("preauth file content wrong: %q", data)
	}

	// Connector URL: first 401, then a retry with Bearer.
	res, err = DownloadAttachmentToSandbox(ctx, c, srv.URL+"/connector", "bild.png", work)
	if err != nil {
		t.Fatalf("connector download: %v", err)
	}
	if !sawToken {
		t.Fatal("a connector URL must be re-fetched with the bearer token")
	}
	if res.ContentType != "image/png" {
		t.Fatalf("connector result wrong: %+v", res)
	}

	// Path traversal in the name is reduced to the basename.
	res, err = DownloadAttachmentToSandbox(ctx, c, srv.URL+"/preauth", "../../etc/passwd", work)
	if err != nil {
		t.Fatalf("traversal download: %v", err)
	}
	if filepath.Dir(res.Path) != filepath.Join(work, "attachments") {
		t.Fatalf("path traversal not neutralized: %s", res.Path)
	}

	// Without a sandbox workdir → error.
	if _, err := DownloadAttachmentToSandbox(ctx, c, srv.URL+"/preauth", "x", ""); err == nil {
		t.Fatal("without a workdir download_attachment must fail")
	}
}

func TestDownloadAttachmentDispatch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /file", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("hallo"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	work := t.TempDir()
	ctx := target.WithWorkdir(context.Background(), work)
	cred := target.Credential{Token: "app:secret"}
	out, err := (System{}).Execute(ctx, "download_attachment",
		json.RawMessage(`{"url":"`+srv.URL+`/file","name":"notiz.txt"}`), cred)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if r, ok := out.(DownloadResult); !ok || r.Filename != "notiz.txt" {
		t.Fatalf("dispatch result: %#v", out)
	}
	// Missing url → validation error.
	if _, err := (System{}).Execute(ctx, "download_attachment", json.RawMessage(`{}`), cred); err == nil {
		t.Fatal("download_attachment without a url must be an error")
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

// fakeConnector plays token endpoint + Bot Connector in one server.
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
		t.Fatalf("send answer: %#v", out)
	}
	if len(rec) == 0 || rec[0] != "send 19:c auth=Bearer connector-token" {
		t.Fatalf("send request wrong: %v", rec)
	}

	// reply with an activity id
	if _, err := sys.Execute(ctx, "reply", json.RawMessage(
		`{"service_url":"`+srv.URL+`","conversation_id":"19:c","reply_to_activity_id":"a9","text":"hi"}`), cred); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if rec[len(rec)-1] != "reply 19:c/a9" {
		t.Fatalf("reply request wrong: %v", rec)
	}

	// reply without an activity id falls back to send
	if _, err := sys.Execute(ctx, "reply", json.RawMessage(
		`{"service_url":"`+srv.URL+`","conversation_id":"19:c","text":"hi"}`), cred); err != nil {
		t.Fatalf("reply fallback: %v", err)
	}
	if rec[len(rec)-1] != "send 19:c auth=Bearer connector-token" {
		t.Fatalf("reply without an activity id must send: %v", rec)
	}

	// create_conversation (POST /conversations + the subsequent send)
	if _, err := sys.Execute(ctx, "create_conversation", json.RawMessage(
		`{"service_url":"`+srv.URL+`","tenant_id":"t1","user_id":"29:u","text":"servus"}`), cred); err != nil {
		t.Fatalf("create_conversation: %v", err)
	}
	if rec[len(rec)-2] != "create-conv" || rec[len(rec)-1] != "send 19:new auth=Bearer connector-token" {
		t.Fatalf("create_conversation flow wrong: %v", rec)
	}
}

func TestExecuteValidation(t *testing.T) {
	sys := System{}
	cred := target.Credential{Token: "app:secret"}
	if _, err := sys.Execute(context.Background(), "send",
		json.RawMessage(`{"conversation_id":"c","text":"x"}`), cred); err == nil {
		t.Fatal("send without a service_url must be an error")
	}
	if _, err := sys.Execute(context.Background(), "unbekannt",
		json.RawMessage(`{}`), cred); err == nil {
		t.Fatal("an unknown action must be an error")
	}
	if _, err := sys.Execute(context.Background(), "send",
		json.RawMessage(`{}`), target.Credential{Token: "kaputt"}); err == nil {
		t.Fatal("a broken credential must be an error")
	}
}

// TestResolveInWorkdir: an agent may only send files from its own working
// directory. Absolute paths and ".." do not lead out of it — otherwise a
// manipulated source could get it to push /etc/passwd into a chat.
func TestResolveInWorkdir(t *testing.T) {
	work := t.TempDir()
	if _, err := resolveInWorkdir(work, "bericht.pdf"); err != nil {
		t.Fatalf("a normal path must work: %v", err)
	}
	if _, err := resolveInWorkdir(work, "subfolder/../bericht.pdf"); err != nil {
		t.Fatalf("a cleaned path inside the workdir must work: %v", err)
	}
	for _, bad := range []string{"../../etc/passwd", "/etc/passwd", "..", ""} {
		if _, err := resolveInWorkdir(work, bad); err == nil {
			t.Fatalf("path %q must be rejected", bad)
		}
	}
	if _, err := resolveInWorkdir("", "bericht.pdf"); err == nil {
		t.Fatal("without a workdir the action must fail")
	}
}

// TestSendFileBrauchtDatei: send_file/upload_file fail cleanly when the named
// file is missing — instead of putting an empty card into the chat.
func TestSendFileBrauchtDatei(t *testing.T) {
	work := t.TempDir()
	c, err := NewClient(target.Credential{Token: "app:pass"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RequestFileConsent(context.Background(), c, "http://x", "19:c", "fehlt.pdf", "", work); err == nil {
		t.Fatal("a missing file must be an error")
	}
	if _, err := UploadConsentedFile(context.Background(), c,
		UploadInput{UploadURL: "http://x/up", Path: "fehlt.pdf"}, work); err == nil {
		t.Fatal("a missing file must be an error")
	}
}

// consentInvoke builds the invoke activity with which Teams delivers the
// recipient's decision. key is the context.key from our card — the path
// send_file asked for.
func consentInvoke(action, key, uploadURL string) []byte {
	return []byte(fmt.Sprintf(`{"type":"invoke","id":"inv-1","name":"fileConsent/invoke",
		"serviceUrl":"https://smba.example/emea/","channelId":"msteams",
		"from":{"id":"29:user","name":"Alice"},"recipient":{"id":"28:bot","name":"Covey"},
		"conversation":{"id":"19:c","conversationType":"personal","tenantId":"t1"},
		"value":{"type":"fileUpload","action":%q,"context":{"key":%q},
			"uploadInfo":{"uploadUrl":%q,"contentUrl":"https://sp.example/f","name":"bericht.pdf",
				"uniqueId":"u-1","fileType":"pdf"}}}`, action, key, uploadURL))
}

// TestConsentEventFuelltPfad: the context.key comes from our own card and
// carries the requested path — the wake prompt puts it into the ready-made
// upload_file call instead of letting the agent guess. The path may sit in a
// subfolder; the basename alone would point nowhere there.
func TestConsentEventFuelltPfad(t *testing.T) {
	ev, err := System{}.ParseWebhook(consentInvoke("accept", "berichte/q3.pdf", "https://sp.example/up"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ev.ResumeInput, `"path":"berichte/q3.pdf"`) {
		t.Fatalf("the path from context.key is missing in the call: %s", ev.ResumeInput)
	}
	if strings.Contains(ev.ResumeInput, "<your file>") {
		t.Fatalf("placeholder despite a known path: %s", ev.ResumeInput)
	}
	if ev.CorrelationKey != CorrelationKey("19:c") || !ev.Wake {
		t.Fatalf("a consent must wake via the conversation: %+v", ev)
	}
	// Without a key (foreign card, old flow) the placeholder stays — better a
	// visible hole than an invented path.
	ohne, err := System{}.ParseWebhook(consentInvoke("accept", "", "https://sp.example/up"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ohne.ResumeInput, "<your file>") {
		t.Fatalf("without a context.key the placeholder must stay: %s", ohne.ResumeInput)
	}
}

// TestConsentEventNurKorrelieren: a consent is the continuation of work that
// was started. If nobody is parked on it, no new task may arise from it —
// otherwise an unsuspecting agent would get the order to upload a file it
// knows nothing about. Holds for both decisions.
func TestConsentEventNurKorrelieren(t *testing.T) {
	for _, tc := range []struct{ name, action, uploadURL string }{
		{"consent", "accept", "https://sp.example/up"},
		{"decline", "decline", ""},
		{"consent without upload url", "accept", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev, err := System{}.ParseWebhook(consentInvoke(tc.action, "bericht.pdf", tc.uploadURL))
			if err != nil {
				t.Fatal(err)
			}
			if !ev.CorrelateOnly {
				t.Fatal("a consent event must not create a new task")
			}
			if !ev.Wake {
				t.Fatal("a consent event must wake the parked agent")
			}
		})
	}
}
