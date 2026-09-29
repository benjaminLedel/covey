package push

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"covey/internal/settings"
)

func TestRelayPassesGoneBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != RelayPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var m Message
		_ = json.NewDecoder(r.Body).Decode(&m)
		if m.Token == "gone" {
			w.WriteHeader(http.StatusGone)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	r := NewRelay(srv.URL + "/")
	if err := r.Send(context.Background(), Message{Token: "ok", Environment: "production", Title: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Send(context.Background(), Message{Token: "gone", Environment: "production", Title: "x"}); !errors.Is(err, ErrGone) {
		t.Fatalf("gone: %v", err)
	}
}

func TestComposeSaysWhoAndWhat(t *testing.T) {
	if title, body := Compose("de", "question", "Bea", "Welches Konto?", false); title != "Bea hat eine Rückfrage" || body != "" {
		t.Fatalf("without preview: %q %q", title, body)
	}
	if title, body := Compose("de", "question", "Bea", "Welches Konto?", true); title != "Bea · Rückfrage" || body != "Welches Konto?" {
		t.Fatalf("with preview: %q %q", title, body)
	}
	if title, _ := Compose("xx", "result", "Bea", "", true); title != "Bea finished a task" {
		t.Fatalf("an unknown language falls back to English: %q", title)
	}
	if title, _ := Compose("ja", "answer", "ベア", "", false); title != "ベアが返信しました" {
		t.Fatalf("ja: %q", title)
	}
}

func TestSoundsAreNamesFromTheBundle(t *testing.T) {
	for pref, want := range map[string]string{
		"bot": "covey-bot-question.caf", "glas": "covey-glas-question.caf", "system": "default", "none": "", "?": "covey-bot-question.caf",
	} {
		if got := SoundFor(pref, "question"); got != want {
			t.Errorf("%s: %q, want %q", pref, got, want)
		}
	}
	ok := Message{Token: "t", Environment: "production", Title: "x", Sound: "covey-schar-error.caf"}
	if !ok.Valid() {
		t.Fatal("a bundle sound is valid")
	}
	ok.Sound = "../../etc/passwd"
	if ok.Valid() {
		t.Fatal("a relay takes only the app's own sound names")
	}
}

func fcmCredentials(t *testing.T, tokenURI string) (*rsa.PrivateKey, string) {
	t.Helper()
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	raw, _ := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "covey-test", "client_email": "push@covey-test.iam.example.org",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "token_uri": tokenURI,
	})
	return k, string(raw)
}

func TestFCMSendsADataMessage(t *testing.T) {
	var key *rsa.PrivateKey
	var exchanges, sends int
	var claims jwt.MapClaims
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			exchanges++
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
				t.Errorf("grant: %q", r.Form.Get("grant_type"))
			}
			tok, err := jwt.Parse(r.Form.Get("assertion"), func(*jwt.Token) (any, error) { return &key.PublicKey, nil })
			if err != nil {
				t.Errorf("assertion: %v", err)
			} else {
				claims = tok.Claims.(jwt.MapClaims)
			}
			_, _ = w.Write([]byte(`{"access_token":"at-1","expires_in":3599,"token_type":"Bearer"}`))
		case "/v1/projects/covey-test/messages:send":
			sends++
			if r.Header.Get("Authorization") != "Bearer at-1" {
				t.Errorf("authorization: %q", r.Header.Get("Authorization"))
			}
			var body struct {
				Message map[string]any `json:"message"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			sent = body.Message
			switch sent["token"] {
			case "gone":
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND",
					"details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`))
			case "garbage":
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"code":400,"message":"The registration token is not a valid FCM registration token","status":"INVALID_ARGUMENT"}}`))
			case "stale":
				w.WriteHeader(http.StatusUnauthorized)
			default:
				_, _ = w.Write([]byte(`{"name":"projects/covey-test/messages/1"}`))
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	key, raw := fcmCredentials(t, srv.URL+"/token")
	f, err := ParseFCM(raw)
	if err != nil {
		t.Fatal(err)
	}
	f.Endpoint = srv.URL
	m := Message{Token: "dev-1", Platform: "android", Environment: "production", Title: "Bea hat geantwortet",
		Badge: 3, AgentID: "a1", Sound: "covey-glas-answer.caf"}
	if err := f.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if claims["iss"] != "push@covey-test.iam.example.org" || claims["scope"] != fcmScope || claims["aud"] != srv.URL+"/token" {
		t.Fatalf("assertion claims: %v", claims)
	}
	data := sent["data"].(map[string]any)
	if data["title"] != "Bea hat geantwortet" || data["agent_id"] != "a1" || data["badge"] != "3" ||
		data["sound"] != "covey-glas-answer.caf" || data["body"] != "" {
		t.Fatalf("data: %v", data)
	}
	if _, shown := sent["notification"]; shown {
		t.Fatal("a data message: the app builds the notification")
	}
	if sent["android"].(map[string]any)["priority"] != "high" {
		t.Fatalf("android: %v", sent["android"])
	}
	for _, tok := range []string{"gone", "garbage"} {
		m.Token = tok
		if err := f.Send(context.Background(), m); !errors.Is(err, ErrGone) {
			t.Fatalf("%s: %v", tok, err)
		}
	}
	if exchanges != 1 {
		t.Fatalf("the access token is kept: %d exchanges", exchanges)
	}
	m.Token = "stale"
	if err := f.Send(context.Background(), m); err == nil || errors.Is(err, ErrGone) {
		t.Fatalf("401 is an error, not a gone device: %v", err)
	}
	m.Token = "dev-1"
	if err := f.Send(context.Background(), m); err != nil || exchanges != 2 {
		t.Fatalf("after a 401 the token is fetched anew: %v, %d exchanges", err, exchanges)
	}
}

func TestFCMRejectsWhatIsNoServiceAccount(t *testing.T) {
	if _, err := ParseFCM(`{"project_info":{"project_id":"covey-test"}}`); err == nil {
		t.Fatal("the app's google-services.json is not the server's credentials")
	}
}

// fakeGoogle answers the token exchange and records what is sent.
func fakeGoogle(t *testing.T) (*httptest.Server, *[]map[string]any, *int) {
	t.Helper()
	var sent []map[string]any
	exchanges := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			exchanges++
			_, _ = w.Write([]byte(`{"access_token":"at-1","expires_in":3599}`))
		case strings.HasSuffix(r.URL.Path, "/messages:send"):
			var body struct {
				Message map[string]any `json:"message"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			sent = append(sent, body.Message)
			_, _ = w.Write([]byte(`{"name":"projects/covey-test/messages/1"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &sent, &exchanges
}

// An iPhone's message is one Firebase hands to Apple as it stands: iOS shows
// it without the app running, groups it by agent, and the app finds the
// agent in userInfo (#431).
func TestFCMSendsAnAPNsMessageToTheIPhone(t *testing.T) {
	srv, sent, _ := fakeGoogle(t)
	_, raw := fcmCredentials(t, srv.URL+"/token")
	f, err := ParseFCM(raw)
	if err != nil {
		t.Fatal(err)
	}
	f.Endpoint = srv.URL
	for _, platform := range []string{"ios", ""} {
		if err := f.Send(context.Background(), Message{Token: "fcm-1", Platform: platform, Environment: "production",
			Title: "Bea hat geantwortet", Badge: 2, AgentID: "a1", Sound: "covey-bot-answer.caf"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Send(context.Background(), Message{Token: "fcm-1", Platform: "ios", Title: "x", Body: "Welches Konto?"}); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 3 {
		t.Fatalf("sent %d", len(*sent))
	}
	for _, m := range (*sent)[:2] {
		if m["token"] != "fcm-1" || m["data"] != nil || m["android"] != nil || m["notification"] != nil {
			t.Fatalf("message: %v", m)
		}
		apns := m["apns"].(map[string]any)
		h := apns["headers"].(map[string]any)
		if h["apns-priority"] != "10" || h["apns-push-type"] != "alert" {
			t.Fatalf("headers: %v", h)
		}
		payload := apns["payload"].(map[string]any)
		aps := payload["aps"].(map[string]any)
		alert := aps["alert"].(map[string]any)
		if alert["title"] != "Bea hat geantwortet" || aps["badge"].(float64) != 2 || aps["thread-id"] != "a1" ||
			aps["sound"] != "covey-bot-answer.caf" || payload["agent_id"] != "a1" {
			t.Fatalf("payload: %v", payload)
		}
		if _, hasBody := alert["body"]; hasBody {
			t.Fatal("no body without preview")
		}
	}
	aps := (*sent)[2]["apns"].(map[string]any)["payload"].(map[string]any)["aps"].(map[string]any)
	if aps["alert"].(map[string]any)["body"] != "Welches Konto?" {
		t.Fatalf("with preview: %v", aps)
	}
	if _, sound := aps["sound"]; sound {
		t.Fatal("the sound none is no sound key")
	}
}

func TestFCMCheckFetchesAToken(t *testing.T) {
	srv, _, exchanges := fakeGoogle(t)
	_, raw := fcmCredentials(t, srv.URL+"/token")
	f, _ := ParseFCM(raw)
	if err := f.Check(context.Background()); err != nil || *exchanges != 1 {
		t.Fatalf("check: %v, %d", err, *exchanges)
	}
	if err := f.Check(context.Background()); err != nil || *exchanges != 2 {
		t.Fatalf("a check asks Google anew: %v, %d", err, *exchanges)
	}
	_, bad := fcmCredentials(t, srv.URL+"/nowhere")
	f, _ = ParseFCM(bad)
	if err := f.Check(context.Background()); err == nil {
		t.Fatal("a token URI that refuses is an error")
	}
}

// The provider follows the configuration and keeps the FCM sender — and
// with it the access token — while the account stays the same.
func TestProviderRebuildsOnChange(t *testing.T) {
	srv, _, exchanges := fakeGoogle(t)
	_, raw := fcmCredentials(t, srv.URL+"/token")
	ctx := context.Background()
	p := &Provider{FCMEndpoint: srv.URL}

	own, err := p.Sender(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := own.(*Relay); !ok || r.URL != "https://app.covey.work" {
		t.Fatalf("with nothing configured: the public relay, got %#v", own)
	}
	if others, _ := p.Relaying(ctx); others != nil {
		t.Fatal("a relaying instance does not relay for others")
	}

	p.Env = settings.PushEnv{Credentials: raw}
	own, _ = p.Sender(ctx)
	f, ok := own.(*FCM)
	if !ok {
		t.Fatalf("with an account: direct, got %#v", own)
	}
	if others, _ := p.Relaying(ctx); others != nil {
		t.Fatal("relaying for others is off unless switched on")
	}
	if err := own.Send(ctx, Message{Token: "t", Platform: "ios", Title: "x"}); err != nil {
		t.Fatal(err)
	}
	p.Env.RelayAccept = true
	others, _ := p.Relaying(ctx)
	if others != Sender(f) {
		t.Fatal("relaying for others uses the same sender")
	}
	if err := others.Send(ctx, Message{Token: "t", Platform: "android", Title: "x"}); err != nil || *exchanges != 1 {
		t.Fatalf("the token survives a change that leaves the account alone: %v, %d", err, *exchanges)
	}

	p.Env = settings.PushEnv{RelayURL: "off"}
	if own, _ := p.Sender(ctx); own != nil {
		t.Fatalf("off: %#v", own)
	}
	p.Env = settings.PushEnv{RelayURL: "https://relay.example.org/"}
	own, _ = p.Sender(ctx)
	if r, ok := own.(*Relay); !ok || r.URL != "https://relay.example.org" {
		t.Fatalf("the environment's relay: %#v", own)
	}
}
