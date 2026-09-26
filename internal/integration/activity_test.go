package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/notes"
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

// TestADayHasOneReview (#368): writing the review of a day again replaces
// its note; the days list says which days have one, in the person's zone.
func TestADayHasOneReview(t *testing.T) {
	s := newStack(t)
	ada := login(t, s, "admin@test.local", "admin-passwort")
	ctx := context.Background()

	var humanID, orgID uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT id, org_id FROM humans WHERE email='admin@test.local'`).Scan(&humanID, &orgID); err != nil {
		t.Fatal(err)
	}
	store := notes.NewStore(s.pool)
	day := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	meta := notes.ReviewMeta{Lang: "de", Zone: "offset:120", Through: time.Date(2026, 9, 26, 7, 0, 0, 0, time.UTC)}
	first, err := store.SetReview(ctx, orgID, humanID, day, "Rückblick", "Vormittag: Mails.", meta)
	if err != nil {
		t.Fatal(err)
	}
	if first.ReviewDay == nil || *first.ReviewDay != "2026-09-26" {
		t.Fatalf("the note knows its day: %v", first.ReviewDay)
	}
	// Written again by itself (#369): an empty title keeps the note's own.
	if _, err := s.pool.Exec(ctx, `UPDATE human_notes SET title='Mein Freitag' WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	again, err := store.SetReview(ctx, orgID, humanID, day, "", "Vormittag: Mails. Nachmittag: Angebot.", meta)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || again.Body != "Vormittag: Mails. Nachmittag: Angebot." || again.Title != "Mein Freitag" {
		t.Fatalf("written again, the review replaces the note of its day: %v / %v", first.ID, again)
	}
	other, _ := store.SetReview(ctx, orgID, humanID, day.AddDate(0, 0, -1), "Rückblick", "Gestern.", meta)
	refs, err := store.ReviewsSince(ctx, humanID, day)
	if err != nil || len(refs) != 1 || refs[0].Zone != "offset:120" || refs[0].Lang != "de" || !refs[0].Through.Equal(meta.Through) {
		t.Fatalf("the review records what it was written from: %v %v", refs, err)
	}
	if other.ID == first.ID {
		t.Fatal("another day, another note")
	}

	// 23:30 UTC on the 25th is the 26th in UTC+2.
	late := time.Date(2026, 9, 25, 23, 30, 0, 0, time.UTC)
	ada.expect(http.MethodPost, "/api/v1/me/activity", map[string]any{"sessions": []map[string]any{
		{"started_at": late, "ended_at": late.Add(time.Minute), "app": "Mail"},
		{"started_at": late.Add(10 * time.Hour), "ended_at": late.Add(10*time.Hour + time.Minute), "app": "Safari"},
	}}, http.StatusCreated)
	days := ada.expect(http.MethodGet, "/api/v1/me/activity/days?offset=120", nil, http.StatusOK)["days"].([]any)
	if len(days) != 1 {
		t.Fatalf("both sessions fall on the 26th at UTC+2: %v", days)
	}
	d := days[0].(map[string]any)
	if d["day"] != "2026-09-26" || d["sessions"] != float64(2) || d["review"] != first.ID.String() {
		t.Fatalf("the day, its sessions and its review: %v", d)
	}
	// The later session (09:30 UTC) ends after what the review covers (07:00).
	if d["stale"] != true {
		t.Fatalf("activity after the review makes it stale: %v", d)
	}
	if n := len(ada.expect(http.MethodGet, "/api/v1/me/activity/days", nil, http.StatusOK)["days"].([]any)); n != 2 {
		t.Fatalf("in UTC the sessions are on two days: %d", n)
	}
	ada.expect(http.MethodGet, "/api/v1/me/activity/days?offset=9999", nil, http.StatusBadRequest)
}
