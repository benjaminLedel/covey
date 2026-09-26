package integration

import (
	"net/http"
	"strings"
	"testing"
)

// TestNotesBelongToTheirSeat walks the notetaker (#336): a note is kept,
// found, changed and deleted by the person who wrote it — and by nobody
// else, not even the org admin of the same organisation.
func TestNotesBelongToTheirSeat(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodPost, "/api/v1/users", map[string]string{
		"email": "auditor@test.local", "display_name": "Aud", "role": "auditor", "password": "auditor-passwort",
	}, http.StatusCreated)
	// The weakest role keeps notes too: a note is the person's, not a right.
	aud := login(t, s, "auditor@test.local", "auditor-passwort")

	meeting := aud.expect(http.MethodPost, "/api/v1/me/notes", map[string]any{
		"kind": "meeting", "title": "Weekly", "body": "Ada: Ich schicke das Angebot bis Freitag.", "duration_seconds": 1800,
	}, http.StatusCreated)
	id := meeting["id"].(string)
	aud.expect(http.MethodPost, "/api/v1/me/notes", map[string]any{"kind": "text", "body": "Milch kaufen"}, http.StatusCreated)

	list := aud.expect(http.MethodGet, "/api/v1/me/notes", nil, http.StatusOK)
	if n := len(list["notes"].([]any)); n != 2 {
		t.Fatalf("two notes, newest first: %d", n)
	}
	if _, ok := list["summarize"].(bool); !ok {
		t.Fatalf("the list says whether summaries are possible: %v", list)
	}
	found := aud.expect(http.MethodGet, "/api/v1/me/notes?q=angebot", nil, http.StatusOK)
	if n := len(found["notes"].([]any)); n != 1 {
		t.Fatalf("the search finds the meeting by its text: %d", n)
	}
	if n := len(aud.expect(http.MethodGet, "/api/v1/me/notes?q=100%25", nil, http.StatusOK)["notes"].([]any)); n != 0 {
		t.Fatalf("a %% in the search is a character, not a wildcard: %d", n)
	}

	// Nobody else's: the admin sees none of it and reaches none of it.
	if n := len(admin.expect(http.MethodGet, "/api/v1/me/notes", nil, http.StatusOK)["notes"].([]any)); n != 0 {
		t.Fatalf("the admin's list holds the admin's notes only: %d", n)
	}
	admin.expect(http.MethodGet, "/api/v1/me/notes/"+id, nil, http.StatusNotFound)
	admin.expect(http.MethodPatch, "/api/v1/me/notes/"+id, map[string]any{"title": "mine now"}, http.StatusNotFound)
	admin.expect(http.MethodDelete, "/api/v1/me/notes/"+id, nil, http.StatusNotFound)
	admin.expect(http.MethodPost, "/api/v1/me/notes/"+id+"/summarize", nil, http.StatusNotFound)

	updated := aud.expect(http.MethodPatch, "/api/v1/me/notes/"+id, map[string]any{"title": "Weekly 25.09."}, http.StatusOK)
	if updated["title"] != "Weekly 25.09." || !strings.Contains(updated["body"].(string), "Angebot") {
		t.Fatalf("a title change keeps the text: %v", updated)
	}

	// Validation.
	aud.expect(http.MethodPost, "/api/v1/me/notes", map[string]any{"kind": "text", "body": "   "}, http.StatusBadRequest)
	aud.expect(http.MethodPost, "/api/v1/me/notes", map[string]any{"kind": "audio", "body": "x"}, http.StatusBadRequest)

	// Without a control-plane credential there is no summary, and it says so.
	aud.expect(http.MethodPost, "/api/v1/me/notes/"+id+"/summarize", nil, http.StatusConflict)

	aud.expect(http.MethodDelete, "/api/v1/me/notes/"+id, nil, http.StatusNoContent)
	aud.expect(http.MethodGet, "/api/v1/me/notes/"+id, nil, http.StatusNotFound)
}

// TestANoteHasProperties (#373): status, due date and tags are set, read
// back, checked and cleared through PATCH.
func TestANoteHasProperties(t *testing.T) {
	s := newStack(t)
	c := login(t, s, "admin@test.local", "admin-passwort")
	n := c.expect(http.MethodPost, "/api/v1/me/notes", map[string]any{"kind": "text", "body": "Angebot"}, http.StatusCreated)
	id := n["id"].(string)
	if n["status"] != "" || n["due"] != nil || len(n["tags"].([]any)) != 0 {
		t.Fatalf("a new note has no properties: %v", n)
	}
	got := c.expect(http.MethodPatch, "/api/v1/me/notes/"+id, map[string]any{
		"status": "doing", "due": "2026-10-02", "tags": []string{" Vertrieb", "#kunde", "vertrieb", ""},
	}, http.StatusOK)
	tags := got["tags"].([]any)
	if got["status"] != "doing" || got["due"] != "2026-10-02" || len(tags) != 2 || tags[0] != "Vertrieb" || tags[1] != "kunde" {
		t.Fatalf("set, the tags trimmed and without repeats: %v", got)
	}
	if got["body"] != "Angebot" {
		t.Fatalf("the text stays: %v", got)
	}
	c.expect(http.MethodPatch, "/api/v1/me/notes/"+id, map[string]any{"status": "waiting"}, http.StatusBadRequest)
	c.expect(http.MethodPatch, "/api/v1/me/notes/"+id, map[string]any{"due": "Freitag"}, http.StatusBadRequest)
	many := make([]string, 11)
	for i := range many {
		many[i] = string(rune('a' + i))
	}
	c.expect(http.MethodPatch, "/api/v1/me/notes/"+id, map[string]any{"tags": many}, http.StatusBadRequest)

	cleared := c.expect(http.MethodPatch, "/api/v1/me/notes/"+id, map[string]any{"status": "", "due": "", "tags": []string{}}, http.StatusOK)
	if cleared["status"] != "" || cleared["due"] != nil || len(cleared["tags"].([]any)) != 0 {
		t.Fatalf("cleared: %v", cleared)
	}
	list := c.expect(http.MethodGet, "/api/v1/me/notes", nil, http.StatusOK)["notes"].([]any)
	if _, ok := list[0].(map[string]any)["tags"].([]any); !ok {
		t.Fatalf("the list carries the properties: %v", list[0])
	}
}
