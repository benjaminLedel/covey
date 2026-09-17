package integration

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// The session slides along: whoever works is not logged out in the middle of
// the work. Only an unused session expires.
//
// Before, the lifetime counted from the login — after twelve hours it was
// over, whether someone was typing or not. The test pushes the end artificially
// into the second half of the window (there and only there the middleware
// renews, so that not every request writes) and checks that a normal request
// moves it back again.
func TestSitzungGleitetMit(t *testing.T) {
	s := newStack(t)
	c := login(t, s, "admin@test.local", "admin-passwort")
	ctx := context.Background()

	// The session expires right away — still valid, but in the second half of
	// the TTL window (the stack sets SessionTTL to one hour).
	knapp := time.Now().Add(5 * time.Minute)
	if _, err := s.pool.Exec(ctx, "UPDATE http_sessions SET expires_at=$1", knapp); err != nil {
		t.Fatalf("expires_at setzen: %v", err)
	}

	resp := c.do(http.MethodGet, "/api/v1/auth/me", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("die Sitzung ist noch gültig, erwartet 200, bekam %d", resp.StatusCode)
	}

	var neu time.Time
	if err := s.pool.QueryRow(ctx, "SELECT expires_at FROM http_sessions").Scan(&neu); err != nil {
		t.Fatalf("expires_at lesen: %v", err)
	}
	if !neu.After(knapp.Add(time.Minute)) {
		t.Fatalf("die Sitzung wurde nicht verlängert: %s (vorher %s)", neu, knapp)
	}

	// And the server also sends the new deadline to the browser — a session
	// renewed only in the database would still throw the cookie away.
	var gesetzt bool
	for _, ck := range resp.Cookies() {
		if ck.Name == "covey_session" && ck.MaxAge > 0 {
			gesetzt = true
		}
	}
	if !gesetzt {
		t.Fatal("die Verlängerung kam ohne aufgefrischtes Cookie")
	}
}

// An expired session is not revived: the renewal only extends what is valid.
// Without the condition in the UPDATE an old cookie could bring back a session
// that has long been dead.
func TestAbgelaufeneSitzungBleibtTot(t *testing.T) {
	s := newStack(t)
	c := login(t, s, "admin@test.local", "admin-passwort")
	ctx := context.Background()

	if _, err := s.pool.Exec(ctx, "UPDATE http_sessions SET expires_at=now() - interval '1 minute'"); err != nil {
		t.Fatalf("expires_at setzen: %v", err)
	}

	resp := c.do(http.MethodGet, "/api/v1/auth/me", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("erwartet 401 nach Ablauf, bekam %d", resp.StatusCode)
	}

	var abgelaufen bool
	if err := s.pool.QueryRow(ctx, "SELECT expires_at < now() FROM http_sessions").Scan(&abgelaufen); err != nil {
		t.Fatalf("expires_at lesen: %v", err)
	}
	if !abgelaufen {
		t.Fatal("die abgelaufene Sitzung wurde verlängert")
	}
}
