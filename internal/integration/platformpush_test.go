package integration

// How the installation sends push notifications, set on the platform page
// (#431): only an installation administrator reaches it, the service account
// is sealed and never handed back, and a change applies without a restart.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"covey/internal/accounts"
	"covey/internal/push"
	"covey/internal/settings"
)

func TestPushIsConfiguredOnThePlatformPage(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// As org_admin the page does not exist.
	admin.expect(http.MethodGet, "/api/v1/platform/push", nil, http.StatusNotFound)
	admin.expect(http.MethodPatch, "/api/v1/platform/push", map[string]any{"mode": "off"}, http.StatusNotFound)
	admin.expect(http.MethodPost, "/api/v1/platform/push/test", nil, http.StatusNotFound)
	if err := accounts.New(s.pool).SetPlatformRole(ctx, "admin@test.local", accounts.RoleSystemAdmin); err != nil {
		t.Fatal(err)
	}

	// Nothing configured: through the public relay, and no relay for others.
	got := admin.expect(http.MethodGet, "/api/v1/platform/push", nil, http.StatusOK)
	if got["mode"] != "relay" || got["relay_url"] != settings.DefaultPushRelay || got["relay_accept"] != false ||
		got["credentials"].(map[string]any)["set"] != false {
		t.Fatalf("defaults: %v", got)
	}
	admin.expect(http.MethodPost, push.RelayPath, map[string]any{}, http.StatusNotFound)
	admin.expect(http.MethodPost, "/api/v1/platform/push/test", nil, http.StatusBadRequest)

	// A refused value leaves the others as they were.
	admin.expect(http.MethodPatch, "/api/v1/platform/push",
		map[string]any{"mode": "direct", "relay_url": "http://relay.example.org"}, http.StatusBadRequest)
	if got := admin.expect(http.MethodGet, "/api/v1/platform/push", nil, http.StatusOK); got["mode"] != "relay" {
		t.Fatalf("the mode moved although the address was refused: %v", got)
	}

	// Google, as far as the test and the sender need it.
	var sent []map[string]any
	google := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			_, _ = w.Write([]byte(`{"access_token":"at-1","expires_in":3599}`))
		case strings.HasSuffix(r.URL.Path, "/messages:send"):
			var body struct {
				Message map[string]any `json:"message"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			sent = append(sent, body.Message)
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer google.Close()
	s.srv.Push.FCMEndpoint = google.URL

	// The app's own Firebase file is refused at the upload.
	admin.expect(http.MethodPut, "/api/v1/platform/push/credentials",
		map[string]any{"credentials": `{"project_info":{"project_id":"covey-test"}}`}, http.StatusBadRequest)

	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	privateKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	raw, _ := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "covey-test", "client_email": "push@covey-test.iam.example.org",
		"private_key": privateKey, "token_uri": google.URL + "/token",
	})
	resp := admin.do(http.MethodPut, "/api/v1/platform/push/credentials", map[string]any{"credentials": string(raw)})
	var answer map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&answer)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("storing the account answers %d: %v", resp.StatusCode, answer)
	}
	creds := answer["credentials"].(map[string]any)
	if creds["set"] != true || creds["source"] != "settings" || creds["project_id"] != "covey-test" ||
		creds["client_email"] != "push@covey-test.iam.example.org" {
		t.Fatalf("credentials: %v", creds)
	}

	// Sealed, and handed back by neither route.
	var value *string
	var ciphertext []byte
	if err := s.pool.QueryRow(ctx, `SELECT value, ciphertext FROM system_settings WHERE key=$1`,
		settings.PushCredentials).Scan(&value, &ciphertext); err != nil {
		t.Fatal(err)
	}
	if value != nil || len(ciphertext) == 0 || strings.Contains(string(ciphertext), "PRIVATE KEY") {
		t.Fatal("the service account is not sealed")
	}
	for _, path := range []string{"/api/v1/platform/push", "/api/v1/platform/settings"} {
		r := admin.do(http.MethodGet, path, nil)
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if strings.Contains(string(body), "PRIVATE KEY") || strings.Contains(string(body), privateKey[40:80]) {
			t.Fatalf("%s hands the key back", path)
		}
	}

	// The test asks Google for a token and is recorded.
	admin.expect(http.MethodPost, "/api/v1/platform/push/test", nil, http.StatusOK)
	got = admin.expect(http.MethodGet, "/api/v1/platform/push", nil, http.StatusOK)
	if got["last_test_at"] == "" || got["last_test_error"] != "" {
		t.Fatalf("the test's record: %v", got)
	}

	// Direct, relaying for others: without a restart, the notifier's source
	// is FCM and the relay route delivers.
	admin.expect(http.MethodPatch, "/api/v1/platform/push",
		map[string]any{"mode": "direct", "relay_accept": true}, http.StatusOK)
	sender, err := s.srv.Push.Sender(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sender.(*push.FCM); !ok {
		t.Fatalf("direct: %#v", sender)
	}
	admin.expect(http.MethodPost, push.RelayPath, map[string]any{
		"token": "fcm-1", "platform": "ios", "environment": "production", "title": "Bea hat geantwortet", "agent_id": "a1",
	}, http.StatusAccepted)
	if len(sent) != 1 || sent[0]["apns"] == nil {
		t.Fatalf("relayed to Google as an iPhone message: %v", sent)
	}

	// Off: nothing to send with, nothing relayed.
	admin.expect(http.MethodPatch, "/api/v1/platform/push", map[string]any{"mode": "off"}, http.StatusOK)
	if sender, _ := s.srv.Push.Sender(ctx); sender != nil {
		t.Fatalf("off: %#v", sender)
	}
	admin.expect(http.MethodPost, push.RelayPath, map[string]any{
		"token": "fcm-1", "platform": "ios", "environment": "production", "title": "x",
	}, http.StatusNotFound)

	// Removing the account; the mode stays what it was set to.
	got = admin.expect(http.MethodDelete, "/api/v1/platform/push/credentials", nil, http.StatusOK)
	if got["credentials"].(map[string]any)["set"] != false || got["mode"] != "off" {
		t.Fatalf("after removing: %v", got)
	}
}

// An iPhone registers an FCM token (#431), which knows no sandbox: whatever
// environment an older app sends, the device is production.
func TestAnIPhoneRegistersAnFCMToken(t *testing.T) {
	s := newStack(t)
	admin := teamLogin(t, s)
	admin.expect(http.MethodPost, "/api/v1/me/push/devices", map[string]any{
		"token": "fcm-token:APA91b", "platform": "ios", "environment": "development",
	}, http.StatusNoContent)
	var env string
	if err := s.pool.QueryRow(context.Background(),
		`SELECT environment FROM push_devices WHERE token='fcm-token:APA91b'`).Scan(&env); err != nil {
		t.Fatal(err)
	}
	if env != "production" {
		t.Fatalf("an iPhone's FCM token has no sandbox: %q", env)
	}
}
