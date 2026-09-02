package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
)

// tryRegister presents a registration token and reports the status code.
func tryRegister(t *testing.T, s *stack, token string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"token": token, "description": "a host"})
	resp, err := http.Post(s.http.URL+"/api/runner/v1/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// A registration token enrols a host for a day, and can be listed and taken
// back where it was created. Until #163 it was valid until somebody edited the
// database, and nothing offered the revoke spec/16 promised.
func TestRegistrationTokensExpireAndCanBeRevoked(t *testing.T) {
	s := newStack(t)
	c := login(t, s, "admin@test.local", "admin-passwort")
	ctx := context.Background()

	created := c.expect(http.MethodPost, "/api/v1/runners/registration-tokens", map[string]any{"description": "Frankfurt"}, http.StatusOK)
	token, _ := created["token"].(string)
	if token == "" {
		t.Fatal("no token in the answer")
	}
	list := c.expectList(http.MethodGet, "/api/v1/runners/registration-tokens", nil, http.StatusOK)
	if len(list) != 1 || list[0]["description"] != "Frankfurt" || list[0]["expires_at"] == nil {
		t.Fatalf("the list has to show the token with its expiry: %v", list)
	}
	if _, leaked := list[0]["token"]; leaked {
		t.Fatal("the list must never carry the token itself")
	}

	// Taken back: the next enrolment with it fails at the door.
	id, _ := list[0]["id"].(string)
	c.expect(http.MethodPost, "/api/v1/runners/registration-tokens/"+id+"/revoke", nil, http.StatusOK)
	if got := tryRegister(t, s, token); got != http.StatusUnauthorized {
		t.Fatalf("a revoked token enrolled a host: HTTP %d", got)
	}

	// Expired: the same, without anybody having done anything.
	fresh := c.expect(http.MethodPost, "/api/v1/runners/registration-tokens", nil, http.StatusOK)
	freshToken, _ := fresh["token"].(string)
	if _, err := s.pool.Exec(ctx, `UPDATE runner_registration_tokens SET expires_at = now() - interval '1 minute' WHERE revoked_at IS NULL`); err != nil {
		t.Fatal(err)
	}
	if got := tryRegister(t, s, freshToken); got != http.StatusUnauthorized {
		t.Fatalf("an expired token enrolled a host: HTTP %d", got)
	}
}

// Decommissioning a runner ends its connection. The row went and the connection
// stayed: the host kept receiving starts, with daemon tokens, while its own
// requests failed with 401 (#163).
func TestDecommissioningARunnerDisconnectsIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	s, pool, _ := remoteStack(t, dir)
	c := login(t, s, "admin@test.local", "admin-passwort")
	runnerID := connectRemoteRunner(t, s, pool, dir, nil)

	c.expect(http.MethodDelete, "/api/v1/runners/"+runnerID.String(), nil, http.StatusOK)
	waitFor(t, "the runner has left the pool", 5*time.Second, func() bool {
		for id, l := range pool.LiveFor(s.orgID) {
			if id == runnerID && l.Connected {
				return false
			}
		}
		return true
	})
	// And it stays out: its reconnect fails at the door with the token of a
	// runner that no longer exists.
	time.Sleep(600 * time.Millisecond) // two of its 200 ms backoffs
	for id, l := range pool.LiveFor(s.orgID) {
		if id == runnerID && l.Connected {
			t.Fatal("the decommissioned runner reconnected")
		}
	}
	_ = uuid.Nil
}
