package push

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func testKey(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	path := filepath.Join(t.TempDir(), "AuthKey_TEST.p8")
	_ = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600)
	return k, path
}

func TestAPNsSendsWhatAppleExpects(t *testing.T) {
	key, path := testKey(t)
	var got struct {
		path, topic, kind string
		claims            jwt.MapClaims
		kid               string
		body              map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path, got.topic, got.kind = r.URL.Path, r.Header.Get("apns-topic"), r.Header.Get("apns-push-type")
		tok, err := jwt.Parse(strings.TrimPrefix(r.Header.Get("authorization"), "bearer "),
			func(*jwt.Token) (any, error) { return &key.PublicKey, nil })
		if err != nil {
			t.Errorf("JWT: %v", err)
		} else {
			got.claims, got.kid = tok.Claims.(jwt.MapClaims), tok.Header["kid"].(string)
		}
		_ = json.NewDecoder(r.Body).Decode(&got.body)
		if strings.HasSuffix(r.URL.Path, "/gone") {
			w.WriteHeader(http.StatusGone)
			_, _ = w.Write([]byte(`{"reason":"Unregistered"}`))
		}
	}))
	defer srv.Close()
	a, err := NewAPNs(path, "KEY123", "TEAM456", "work.example.app")
	if err != nil {
		t.Fatal(err)
	}
	a.Development = srv.URL
	err = a.Send(context.Background(), Message{Token: "abc", Environment: "development", Title: "Bea hat geantwortet", Badge: 2, AgentID: "a1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.path != "/3/device/abc" || got.topic != "work.example.app" || got.kind != "alert" {
		t.Fatalf("request: %+v", got)
	}
	if got.kid != "KEY123" || got.claims["iss"] != "TEAM456" {
		t.Fatalf("JWT signed with the key, kid and team: %v %v", got.kid, got.claims)
	}
	aps := got.body["aps"].(map[string]any)
	if aps["badge"].(float64) != 2 || aps["thread-id"] != "a1" || got.body["agent_id"] != "a1" {
		t.Fatalf("payload: %v", got.body)
	}
	if _, hasBody := aps["alert"].(map[string]any)["body"]; hasBody {
		t.Fatal("no body without preview")
	}
	if err := a.Send(context.Background(), Message{Token: "gone", Environment: "development", Title: "x"}); !errors.Is(err, ErrGone) {
		t.Fatalf("an unregistered device is ErrGone: %v", err)
	}
}

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
	path := filepath.Join(t.TempDir(), "service-account.json")
	_ = os.WriteFile(path, raw, 0o600)
	return k, path
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
	key, path := fcmCredentials(t, srv.URL+"/token")
	f, err := NewFCM(path)
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
	path := filepath.Join(t.TempDir(), "google-services.json")
	_ = os.WriteFile(path, []byte(`{"project_info":{"project_id":"covey-test"}}`), 0o600)
	if _, err := NewFCM(path); err == nil {
		t.Fatal("the app's google-services.json is not the server's credentials")
	}
}

type recorder struct{ got []Message }

func (r *recorder) Send(_ context.Context, m Message) error {
	r.got = append(r.got, m)
	return nil
}

func TestSendersPickByPlatform(t *testing.T) {
	apns, fcm, relay := &recorder{}, &recorder{}, &recorder{}
	s := Senders{APNs: apns, FCM: fcm, Fallback: relay}
	for _, p := range []string{"", "ios", "macos", "android"} {
		_ = s.Send(context.Background(), Message{Token: p, Platform: p})
	}
	if len(apns.got) != 3 || len(fcm.got) != 1 || fcm.got[0].Token != "android" || len(relay.got) != 0 {
		t.Fatalf("apns %v, fcm %v, relay %v", apns.got, fcm.got, relay.got)
	}
	s = Senders{APNs: apns, Fallback: relay}
	_ = s.Send(context.Background(), Message{Token: "a", Platform: "android"})
	if len(relay.got) != 1 {
		t.Fatal("without FCM credentials Android goes through the relay")
	}
	s = Senders{APNs: apns}
	if err := s.Send(context.Background(), Message{Token: "a", Platform: "android"}); err == nil {
		t.Fatal("neither a key nor a relay: an error")
	}
	if !(Message{Token: "t", Platform: "android", Environment: "production", Title: "x"}).Valid() ||
		(Message{Token: "t", Platform: "windows", Environment: "production", Title: "x"}).Valid() {
		t.Fatal("platforms a relay takes: ios, macos, android")
	}
}
