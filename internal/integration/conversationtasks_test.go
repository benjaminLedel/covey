package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"covey/internal/backlog"
)

// TestATaskFromAConversationIsReadOnlyByItsMembers: a message to an agent
// becomes a task that carries the message, the conversation before it and the
// agent's reply. Only members read a conversation, and the audit roles read
// it in the audit (spec/28, section 3) — so a seat that may read the backlog
// but is not in the conversation sees the task on the board without its text,
// does not find it by searching, and reads neither its notes, nor the notes
// of its transitions, nor its title in the thread, the work record or the
// brief status.
func TestATaskFromAConversationIsReadOnlyByItsMembers(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("conv-tasks")
	s.ohneLaeufe(agent.ID)
	admin := teamLogin(t, s) // triage off: every message becomes a task
	base := "/api/v1/agents/" + agent.ID.String()
	const geheim = "SECRET-MARKER"

	created := admin.expect(http.MethodPost, base+"/messages",
		map[string]any{"text": geheim + " please look into the salary review"}, http.StatusCreated)
	taskID := uuid.MustParse(created["task"].(map[string]any)["id"].(string))

	// Through a question, an answer and a result: every place the text goes.
	if tk, err := s.backlog.ClaimNext(ctx, agent.ID); err != nil || tk.ID != taskID {
		t.Fatalf("claim: %v %v", tk.ID, err)
	}
	if _, err := s.backlog.Block(ctx, taskID, "q", "sess-1", geheim+" which review?"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.AddNote(ctx, taskID, "agent", geheim+" found the sheet"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.Answer(ctx, taskID, "admin@test.local", geheim+" the spring one"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.ClaimNext(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.Complete(ctx, taskID, backlog.StateDone, geheim+" done, see sheet", ""); err != nil {
		t.Fatal(err)
	}
	// And one still open, for the thread's list of what is running.
	admin.expect(http.MethodPost, base+"/messages", map[string]any{"text": geheim + " and the other one"}, http.StatusCreated)

	s.mitglied(t, "carla@test.local", "Carla", "controlling", "carla-passwort")
	carla := login(t, s, "carla@test.local", "carla-passwort")
	s.mitglied(t, "otto@test.local", "Otto", "agent_owner", "otto-passwort")
	otto := login(t, s, "otto@test.local", "otto-passwort")
	s.mitglied(t, "aud@test.local", "Aud", "auditor", "auditor-passwort")
	aud := login(t, s, "aud@test.local", "auditor-passwort")

	sieht := func(v any) bool {
		raw, _ := json.Marshal(v)
		return strings.Contains(string(raw), geheim)
	}

	// The board: both tasks are there, redacted.
	board := carla.expectList(http.MethodGet, base+"/backlog", nil, http.StatusOK)
	if len(board) != 2 {
		t.Fatalf("the board must keep the tasks: %d", len(board))
	}
	for _, tk := range board {
		if tk["redacted"] != true || tk["title"] != backlog.RedactedTitle || tk["state"] == nil || tk["origin"] == nil {
			t.Fatalf("a non-member gets the task redacted, its state and origin kept: %v", tk)
		}
	}
	if sieht(board) {
		t.Fatalf("the board carries the conversation to a non-member: %v", board)
	}
	if hits := carla.expectList(http.MethodGet, base+"/backlog?q=salary", nil, http.StatusOK); len(hits) != 0 {
		t.Fatalf("the search must not find a task from a conversation the reader is not in: %v", hits)
	}
	if v := carla.expectList(http.MethodGet, "/api/v1/tasks/"+taskID.String()+"/notes", nil, http.StatusOK); sieht(v) {
		t.Fatalf("notes: %v", v)
	}
	if v := carla.expectList(http.MethodGet, "/api/v1/tasks/"+taskID.String()+"/transitions", nil, http.StatusOK); sieht(v) || len(v) == 0 {
		t.Fatalf("transitions must stay, without their notes: %v", v)
	}
	if v := carla.expect(http.MethodGet, base+"/thread", nil, http.StatusOK); sieht(v) {
		t.Fatalf("the thread's running tasks: %v", v)
	}
	if v := otto.expect(http.MethodGet, base+"/work-record", nil, http.StatusOK); sieht(v) {
		t.Fatalf("the work record: %v", v)
	}
	if v := otto.expect(http.MethodGet, "/api/v1/hiring/brief/"+taskID.String(), nil, http.StatusOK); sieht(v) {
		t.Fatalf("the brief status: %v", v)
	}

	// A member reads it; so do the audit roles.
	for name, c := range map[string]*apiClient{"member": admin, "auditor": aud} {
		if v := c.expectList(http.MethodGet, base+"/backlog", nil, http.StatusOK); !sieht(v) {
			t.Fatalf("%s must read the task: %v", name, v)
		}
		if v := c.expectList(http.MethodGet, base+"/backlog?q=salary", nil, http.StatusOK); len(v) == 0 {
			t.Fatalf("%s must find the task: %v", name, v)
		}
		if v := c.expectList(http.MethodGet, "/api/v1/tasks/"+taskID.String()+"/notes", nil, http.StatusOK); !sieht(v) {
			t.Fatalf("%s must read the notes: %v", name, v)
		}
	}

	// A task from carla's own conversation is hers to read.
	carla.expect(http.MethodPost, base+"/messages", map[string]any{"text": "CARLA-OWN budget question"}, http.StatusCreated)
	var eigene bool
	for _, tk := range carla.expectList(http.MethodGet, base+"/backlog", nil, http.StatusOK) {
		eigene = eigene || (tk["redacted"] == nil && strings.Contains(tk["title"].(string), "CARLA-OWN"))
	}
	if !eigene {
		t.Fatal("a member reads the task of their own conversation")
	}
}
