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
		t.Fatalf("erwartet 2 Datei-Anhänge (text/html gefiltert), bekam %d: %+v", len(files), files)
	}
	if files[0].DownloadURL() != "https://share.example/dl/report.pdf" || files[0].Filename() != "report.pdf" {
		t.Fatalf("file.download.info falsch: %+v", files[0])
	}
	if files[1].DownloadURL() != "https://smba.example/v3/attachments/x" {
		t.Fatalf("inline-Bild-URL falsch: %+v", files[1])
	}
	// Nachricht ohne Text, aber mit Anhang muss wecken.
	if !a.ShouldWake() {
		t.Fatal("Nachricht nur mit Anhang muss wecken")
	}
}

func TestDownloadAttachmentToSandbox(t *testing.T) {
	// Server: /preauth liefert direkt (ohne Token), /connector verlangt Bearer.
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

	// Vor-autorisierte URL (kein Token nötig).
	res, err := DownloadAttachmentToSandbox(ctx, c, srv.URL+"/preauth", "report.pdf", work)
	if err != nil {
		t.Fatalf("preauth-Download: %v", err)
	}
	if res.Filename != "report.pdf" || res.ContentType != "application/pdf" || res.Bytes == 0 {
		t.Fatalf("preauth-Ergebnis falsch: %+v", res)
	}
	if data, _ := os.ReadFile(res.Path); string(data) != "%PDF-1.4 fake" {
		t.Fatalf("preauth-Datei-Inhalt falsch: %q", data)
	}

	// Connector-URL: erst 401, dann Retry mit Bearer.
	res, err = DownloadAttachmentToSandbox(ctx, c, srv.URL+"/connector", "bild.png", work)
	if err != nil {
		t.Fatalf("connector-Download: %v", err)
	}
	if !sawToken {
		t.Fatal("Connector-URL muss mit Bearer-Token nachgeladen werden")
	}
	if res.ContentType != "image/png" {
		t.Fatalf("connector-Ergebnis falsch: %+v", res)
	}

	// Pfad-Traversal im Namen wird auf den Basename reduziert.
	res, err = DownloadAttachmentToSandbox(ctx, c, srv.URL+"/preauth", "../../etc/passwd", work)
	if err != nil {
		t.Fatalf("traversal-Download: %v", err)
	}
	if filepath.Dir(res.Path) != filepath.Join(work, "attachments") {
		t.Fatalf("Pfad-Traversal nicht neutralisiert: %s", res.Path)
	}

	// Ohne Sandbox-Workdir → Fehler.
	if _, err := DownloadAttachmentToSandbox(ctx, c, srv.URL+"/preauth", "x", ""); err == nil {
		t.Fatal("ohne Workdir muss download_attachment scheitern")
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
		t.Fatalf("dispatch-Ergebnis: %#v", out)
	}
	// Fehlende url → Validierungsfehler.
	if _, err := (System{}).Execute(ctx, "download_attachment", json.RawMessage(`{}`), cred); err == nil {
		t.Fatal("download_attachment ohne url muss Fehler sein")
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

// TestResolveInWorkdir: ein Agent darf nur Dateien aus seinem eigenen
// Arbeitsverzeichnis verschicken. Absolute Pfade und ".." führen nicht hinaus —
// sonst könnte eine manipulierte Quelle ihn dazu bringen, /etc/passwd in einen
// Chat zu schieben.
func TestResolveInWorkdir(t *testing.T) {
	work := t.TempDir()
	if _, err := resolveInWorkdir(work, "bericht.pdf"); err != nil {
		t.Fatalf("normaler Pfad muss gehen: %v", err)
	}
	if _, err := resolveInWorkdir(work, "unterordner/../bericht.pdf"); err != nil {
		t.Fatalf("bereinigter Pfad innerhalb des Workdirs muss gehen: %v", err)
	}
	for _, bad := range []string{"../../etc/passwd", "/etc/passwd", "..", ""} {
		if _, err := resolveInWorkdir(work, bad); err == nil {
			t.Fatalf("pfad %q muss abgelehnt werden", bad)
		}
	}
	if _, err := resolveInWorkdir("", "bericht.pdf"); err == nil {
		t.Fatal("ohne Workdir muss die Aktion scheitern")
	}
}

// TestSendFileBrauchtDatei: send_file/upload_file scheitern sauber, wenn die
// genannte Datei fehlt — statt eine leere Karte in den Chat zu stellen.
func TestSendFileBrauchtDatei(t *testing.T) {
	work := t.TempDir()
	c, err := NewClient(target.Credential{Token: "app:pass"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RequestFileConsent(context.Background(), c, "http://x", "19:c", "fehlt.pdf", "", work); err == nil {
		t.Fatal("fehlende Datei muss Fehler sein")
	}
	if _, err := UploadConsentedFile(context.Background(), c,
		UploadInput{UploadURL: "http://x/up", Path: "fehlt.pdf"}, work); err == nil {
		t.Fatal("fehlende Datei muss Fehler sein")
	}
}

// consentInvoke baut die invoke-Activity, mit der Teams die Entscheidung des
// Empfängers zustellt. key ist der context.key aus unserer Karte — der Pfad,
// den send_file angefragt hat.
func consentInvoke(action, key, uploadURL string) []byte {
	return []byte(fmt.Sprintf(`{"type":"invoke","id":"inv-1","name":"fileConsent/invoke",
		"serviceUrl":"https://smba.example/emea/","channelId":"msteams",
		"from":{"id":"29:user","name":"Alice"},"recipient":{"id":"28:bot","name":"Covey"},
		"conversation":{"id":"19:c","conversationType":"personal","tenantId":"t1"},
		"value":{"type":"fileUpload","action":%q,"context":{"key":%q},
			"uploadInfo":{"uploadUrl":%q,"contentUrl":"https://sp.example/f","name":"bericht.pdf",
				"uniqueId":"u-1","fileType":"pdf"}}}`, action, key, uploadURL))
}

// TestConsentEventFuelltPfad: der context.key kommt aus unserer eigenen Karte
// und trägt den angefragten Pfad — der Wake-Prompt setzt ihn in den fertigen
// upload_file-Aufruf ein, statt den Agenten raten zu lassen. Der Pfad kann in
// einem Unterordner liegen; der Basename allein zeigte dort ins Leere.
func TestConsentEventFuelltPfad(t *testing.T) {
	ev, err := System{}.ParseWebhook(consentInvoke("accept", "berichte/q3.pdf", "https://sp.example/up"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ev.ResumeInput, `"path":"berichte/q3.pdf"`) {
		t.Fatalf("pfad aus context.key fehlt im Aufruf: %s", ev.ResumeInput)
	}
	if strings.Contains(ev.ResumeInput, "<deine Datei>") {
		t.Fatalf("platzhalter trotz bekanntem pfad: %s", ev.ResumeInput)
	}
	if ev.CorrelationKey != CorrelationKey("19:c") || !ev.Wake {
		t.Fatalf("zustimmung muss über die Konversation wecken: %+v", ev)
	}
	// Ohne key (fremde Karte, alter Flow) bleibt der Platzhalter stehen —
	// besser ein sichtbares Loch als ein erfundener Pfad.
	ohne, err := System{}.ParseWebhook(consentInvoke("accept", "", "https://sp.example/up"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ohne.ResumeInput, "<deine Datei>") {
		t.Fatalf("ohne context.key muss der Platzhalter bleiben: %s", ohne.ResumeInput)
	}
}

// TestConsentEventNurKorrelieren: eine Zustimmung ist die Fortsetzung einer
// angefangenen Arbeit. Parkt niemand darauf, darf daraus keine neue Aufgabe
// entstehen — sonst bekäme ein ahnungsloser Agent den Auftrag, eine Datei
// hochzuladen, von der er nichts weiß. Gilt für beide Entscheidungen.
func TestConsentEventNurKorrelieren(t *testing.T) {
	for _, tc := range []struct{ name, action, uploadURL string }{
		{"zustimmung", "accept", "https://sp.example/up"},
		{"ablehnung", "decline", ""},
		{"zustimmung ohne upload-url", "accept", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev, err := System{}.ParseWebhook(consentInvoke(tc.action, "bericht.pdf", tc.uploadURL))
			if err != nil {
				t.Fatal(err)
			}
			if !ev.CorrelateOnly {
				t.Fatal("consent-Event darf keine neue Aufgabe anlegen")
			}
			if !ev.Wake {
				t.Fatal("consent-Event muss den geparkten Agenten wecken")
			}
		})
	}
}
