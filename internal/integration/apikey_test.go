package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// bearer sends a request authenticated by an API key instead of a cookie —
// deliberately with a bare client, so that no session can quietly stand in for
// the key and make the test pass for the wrong reason.
func bearer(t *testing.T, s *stack, token, method, path string, body any) *http.Response {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, s.http.URL+path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// createKey mints a key through the session and returns its token and id.
func createKey(t *testing.T, c *apiClient, name string, days int) (string, string) {
	t.Helper()
	out := c.expect(http.MethodPost, "/api/v1/auth/api-keys",
		map[string]any{"name": name, "expires_in_days": days}, http.StatusCreated)
	token, _ := out["token"].(string)
	id, _ := out["id"].(string)
	if token == "" || id == "" {
		t.Fatalf("the answer carries no token/id: %v", out)
	}
	return token, id
}

// An API key is the second badge for the human API: what the session cookie can
// do inside a browser, the key does from outside — and it carries exactly the
// rights of the seat it was minted for.
func TestAPIKeyAuthentifiziert(t *testing.T) {
	s := newStack(t)
	c := login(t, s, "admin@test.local", "admin-passwort")

	token, id := createKey(t, c, "Skript", 0)

	// The token is shown once. What stays behind is a prefix you can recognise
	// a key by and nothing you could use.
	keys := c.expectList(http.MethodGet, "/api/v1/auth/api-keys", nil, http.StatusOK)
	if len(keys) != 1 {
		t.Fatalf("expected exactly one key, got %d", len(keys))
	}
	if _, ok := keys[0]["token"]; ok {
		t.Fatal("the list hands out the token — it must exist exactly once, in the answer that created it")
	}
	prefix, _ := keys[0]["prefix"].(string)
	if prefix == "" || len(prefix) >= len(token) {
		t.Fatalf("prefix %q is not a prefix of the token", prefix)
	}

	// The key works on an ordinary route.
	resp := bearer(t, s, token, http.MethodGet, "/api/v1/agents", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the key does not authenticate: HTTP %d", resp.StatusCode)
	}

	// And it stops working the moment it is revoked.
	c.expect(http.MethodDelete, "/api/v1/auth/api-keys/"+id, nil, http.StatusOK)
	resp = bearer(t, s, token, http.MethodGet, "/api/v1/agents", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a revoked key still authenticates: HTTP %d", resp.StatusCode)
	}
}

// A leaked key must not be able to entrench itself: it may neither mint another
// key nor replace the password. Both need the browser session, and therefore
// the password.
func TestAPIKeyKannSichNichtVerewigen(t *testing.T) {
	s := newStack(t)
	c := login(t, s, "admin@test.local", "admin-passwort")
	token, _ := createKey(t, c, "Skript", 0)

	for _, fall := range []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"a second key", http.MethodPost, "/api/v1/auth/api-keys", map[string]any{"name": "noch einer"}},
		{"a new password", http.MethodPatch, "/api/v1/auth/me",
			map[string]any{"password": "neues-passwort", "current_password": "admin-passwort"}},
	} {
		resp := bearer(t, s, token, fall.method, fall.path, fall.body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s: the key was allowed through with HTTP %d, expected 403", fall.name, resp.StatusCode)
		}
	}

	// The harmless half of the same route stays open — a key may say who it is.
	resp := bearer(t, s, token, http.MethodPatch, "/api/v1/auth/me", map[string]any{"display_name": "Skript"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("changing the display name must work with a key: HTTP %d", resp.StatusCode)
	}
}

// The key carries the role of its seat — not more. An auditor's key reads and
// does not write, exactly like the auditor in the browser.
func TestAPIKeyTraegtDieRolleDesSitzes(t *testing.T) {
	s := newStack(t)
	s.mitglied(t, "auditor@test.local", "Auditor", "auditor", "auditor-passwort")
	c := login(t, s, "auditor@test.local", "auditor-passwort")
	token, _ := createKey(t, c, "Nur lesen", 0)

	resp := bearer(t, s, token, http.MethodGet, "/api/v1/agents", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the auditor may read: HTTP %d", resp.StatusCode)
	}

	resp = bearer(t, s, token, http.MethodPost, "/api/v1/agents",
		map[string]any{"slug": "neu", "display_name": "Neu", "runtime": "claude-code"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("the auditor must not create an agent, not with a key either: HTTP %d", resp.StatusCode)
	}
}

// An expired key is as good as none. The check sits in the query, so that no
// caller can forget it.
func TestAPIKeyLaeuftAb(t *testing.T) {
	s := newStack(t)
	c := login(t, s, "admin@test.local", "admin-passwort")
	token, _ := createKey(t, c, "Kurzlebig", 1)

	if _, err := s.pool.Exec(context.Background(),
		"UPDATE api_keys SET expires_at = now() - interval '1 minute'"); err != nil {
		t.Fatalf("expires_at setzen: %v", err)
	}
	resp := bearer(t, s, token, http.MethodGet, "/api/v1/agents", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an expired key still authenticates: HTTP %d", resp.StatusCode)
	}
}
