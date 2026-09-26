package integration

import (
	"net/http"
	"testing"
)

// TestSetupCanBeFinishedAsItStands (#394): setup that someone closed counts
// as closed whatever its cards say, and can be reopened.
func TestSetupCanBeFinishedAsItStands(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	if st := admin.expect(http.MethodGet, "/api/v1/setup/state", nil, http.StatusOK); st["closed"] != false {
		t.Fatalf("a new organisation's setup is not closed: %v", st["closed"])
	}
	admin.expect(http.MethodPost, "/api/v1/setup/close", nil, http.StatusOK)
	if st := admin.expect(http.MethodGet, "/api/v1/setup/state", nil, http.StatusOK); st["closed"] != true {
		t.Fatalf("closed: %v", st["closed"])
	}
	admin.expect(http.MethodPost, "/api/v1/setup/reopen", nil, http.StatusOK)
	if st := admin.expect(http.MethodGet, "/api/v1/setup/state", nil, http.StatusOK); st["closed"] != false {
		t.Fatalf("reopened: %v", st["closed"])
	}
}
