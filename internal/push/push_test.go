package push

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
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
