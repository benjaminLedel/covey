package integration

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// Die Sitzung gleitet mit: Wer arbeitet, wird nicht mitten in der Arbeit
// abgemeldet. Nur eine ungenutzte Sitzung läuft ab.
//
// Vorher zählte die Lebensdauer ab der Anmeldung — nach zwölf Stunden war
// Schluss, egal ob jemand gerade tippte. Der Test schiebt das Ende künstlich in
// die zweite Hälfte des Fensters (dort und erst dort erneuert die Middleware,
// damit nicht jede Anfrage schreibt) und prüft, dass eine normale Anfrage es
// wieder nach hinten setzt.
func TestSitzungGleitetMit(t *testing.T) {
	s := newStack(t)
	c := login(t, s, "admin@test.local", "admin-passwort")
	ctx := context.Background()

	// Die Sitzung ist gleich abgelaufen — noch gültig, aber in der zweiten
	// Hälfte des TTL-Fensters (der Stack setzt SessionTTL auf eine Stunde).
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

	// Und der Server schickt die neue Frist auch an den Browser — eine nur in
	// der Datenbank verlängerte Sitzung würfe das Cookie trotzdem weg.
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

// Eine abgelaufene Sitzung wird nicht wiederbelebt: Die Erneuerung verlängert
// nur, was gilt. Ohne die Bedingung im UPDATE könnte ein alter Cookie eine
// längst tote Sitzung zurückholen.
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
