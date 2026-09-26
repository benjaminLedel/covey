package integration

import (
	"net/http"
	"testing"
	"time"
)

// TestActivityBelongsToItsSeat (#363): the activity log is kept, read by day
// in the person's time zone, and deleted by the person who recorded it —
// and read by nobody else, org admin included: it must never become
// monitoring.
func TestActivityBelongsToItsSeat(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodPost, "/api/v1/users", map[string]string{
		"email": "ada@test.local", "display_name": "Ada", "role": "auditor", "password": "ada-passwort",
	}, http.StatusCreated)
	ada := login(t, s, "ada@test.local", "ada-passwort")

	// 23:30 UTC on the 25th is 01:30 on the 26th in Berlin: the day is the
	// person's, not the server's.
	late := time.Date(2026, 9, 25, 23, 30, 0, 0, time.UTC)
	morning := time.Date(2026, 9, 26, 7, 10, 0, 0, time.UTC)
	ada.expect(http.MethodPost, "/api/v1/me/activity", map[string]any{"sessions": []map[string]any{
		{"started_at": late, "ended_at": late.Add(10 * time.Minute), "app": "Mail", "window": "Re: Angebot", "excerpt": "Hallo Ada"},
		{"started_at": morning, "ended_at": morning.Add(20 * time.Minute), "app": "Safari", "url": "https://example.org"},
	}}, http.StatusCreated)

	day := ada.expect(http.MethodGet, "/api/v1/me/activity?day=2026-09-26&tz=Europe/Berlin", nil, http.StatusOK)
	if n := len(day["sessions"].([]any)); n != 2 {
		t.Fatalf("both sessions fall on the 26th in Berlin: %d", n)
	}
	utc := ada.expect(http.MethodGet, "/api/v1/me/activity?day=2026-09-26", nil, http.StatusOK)
	if n := len(utc["sessions"].([]any)); n != 1 {
		t.Fatalf("in UTC only the morning is on the 26th: %d", n)
	}

	// Nobody else's.
	if n := len(admin.expect(http.MethodGet, "/api/v1/me/activity?day=2026-09-26&tz=Europe/Berlin", nil, http.StatusOK)["sessions"].([]any)); n != 0 {
		t.Fatalf("the admin reads the admin's log only: %d", n)
	}
	admin.expect(http.MethodDelete, "/api/v1/me/activity", nil, http.StatusOK)
	if n := len(ada.expect(http.MethodGet, "/api/v1/me/activity?day=2026-09-26&tz=Europe/Berlin", nil, http.StatusOK)["sessions"].([]any)); n != 2 {
		t.Fatalf("the admin's delete reaches only the admin's log: %d", n)
	}

	// Validation.
	ada.expect(http.MethodPost, "/api/v1/me/activity", map[string]any{"sessions": []map[string]any{
		{"started_at": morning, "ended_at": morning.Add(-time.Minute), "app": "Mail"},
	}}, http.StatusBadRequest)
	ada.expect(http.MethodPost, "/api/v1/me/activity", map[string]any{"sessions": []any{}}, http.StatusBadRequest)
	ada.expect(http.MethodGet, "/api/v1/me/activity?day=gestern", nil, http.StatusBadRequest)
	ada.expect(http.MethodGet, "/api/v1/me/activity?day=2026-09-26&tz=Mars/Olympus", nil, http.StatusBadRequest)

	// Without a control-plane credential there is no review, and it says so;
	// a day without activity has none either.
	ada.expect(http.MethodPost, "/api/v1/me/activity/review?day=2026-09-26&tz=Europe/Berlin", map[string]any{"lang": "de", "title": "Rückblick"}, http.StatusConflict)
	ada.expect(http.MethodPost, "/api/v1/me/activity/review?day=2026-09-20", map[string]any{"lang": "de"}, http.StatusNotFound)

	// Old sessions go as new ones arrive (default retention: 14 days).
	old := time.Now().Add(-20 * 24 * time.Hour)
	ada.expect(http.MethodPost, "/api/v1/me/activity", map[string]any{"sessions": []map[string]any{
		{"started_at": old, "ended_at": old.Add(time.Minute), "app": "Mail"},
	}}, http.StatusCreated)
	oldDay := old.UTC().Format("2006-01-02")
	if n := len(ada.expect(http.MethodGet, "/api/v1/me/activity?day="+oldDay, nil, http.StatusOK)["sessions"].([]any)); n != 0 {
		t.Fatalf("a session older than the retention is gone with the write that brought it: %d", n)
	}

	// The person deletes a day, then everything.
	if d := ada.expect(http.MethodDelete, "/api/v1/me/activity?day=2026-09-26&tz=UTC", nil, http.StatusOK)["deleted"].(float64); d != 1 {
		t.Fatalf("one session on the 26th in UTC: %v", d)
	}
	ada.expect(http.MethodDelete, "/api/v1/me/activity", nil, http.StatusOK)
	if n := len(ada.expect(http.MethodGet, "/api/v1/me/activity?day=2026-09-25", nil, http.StatusOK)["sessions"].([]any)); n != 0 {
		t.Fatalf("everything is gone: %d", n)
	}
}
