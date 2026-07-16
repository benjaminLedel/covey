package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckCredentialWrongSlot(t *testing.T) {
	// Falsch einsortierte Werte werden ohne Netz erkannt.
	c := checkCredential(context.Background(), "claude_code_oauth_token", "sk-ant-api03-xxx")
	if !c.Checked || c.Valid || !strings.Contains(c.Hint, "anthropic_api_key") {
		t.Errorf("API-Key im OAuth-Slot nicht erkannt: %+v", c)
	}
	c = checkCredential(context.Background(), "anthropic_api_key", "sk-ant-oat01-xxx")
	if !c.Checked || c.Valid || !strings.Contains(c.Hint, "claude_code_oauth_token") {
		t.Errorf("OAuth-Token im API-Key-Slot nicht erkannt: %+v", c)
	}
}

func TestCheckCredentialLive(t *testing.T) {
	var gotAuth, gotBeta, gotAPIKey string
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotBeta = r.Header.Get("anthropic-beta")
		gotAPIKey = r.Header.Get("x-api-key")
		w.WriteHeader(status)
	}))
	defer srv.Close()
	orig := anthropicBaseURL
	anthropicBaseURL = srv.URL
	defer func() { anthropicBaseURL = orig }()

	// OAuth-Token: Bearer + Beta-Header, 200 → gültig.
	c := checkCredential(context.Background(), "claude_code_oauth_token", "sk-ant-oat01-live")
	if !c.Checked || !c.Valid {
		t.Errorf("gültiges OAuth-Token: %+v", c)
	}
	if gotAuth != "Bearer sk-ant-oat01-live" || gotBeta == "" {
		t.Errorf("OAuth-Header falsch: auth=%q beta=%q", gotAuth, gotBeta)
	}

	// API-Key: x-api-key-Header.
	c = checkCredential(context.Background(), "anthropic_api_key", "sk-ant-api03-live")
	if !c.Checked || !c.Valid || gotAPIKey != "sk-ant-api03-live" {
		t.Errorf("gültiger API-Key: %+v (x-api-key=%q)", c, gotAPIKey)
	}

	// 401 → ungültig, mit actionable Hinweis.
	status = http.StatusUnauthorized
	c = checkCredential(context.Background(), "claude_code_oauth_token", "sk-ant-oat01-dead")
	if !c.Checked || c.Valid || !strings.Contains(c.Hint, "claude setup-token") {
		t.Errorf("abgelaufenes OAuth-Token: %+v", c)
	}

	// Serverfehler → gespeichert, aber ungeprüft (fail-open fürs Speichern).
	status = http.StatusBadGateway
	c = checkCredential(context.Background(), "anthropic_api_key", "sk-ant-api03-x")
	if c.Checked {
		t.Errorf("5xx darf nicht als geprüft gelten: %+v", c)
	}

	// Unbekannte Keys werden nicht geprüft.
	c = checkCredential(context.Background(), "zammad_token", "abc")
	if c.Checked || c.Hint != "" {
		t.Errorf("unbekannter Key geprüft: %+v", c)
	}
}
