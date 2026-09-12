package integration

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// A task is a first-class object, and what can be done to it by hand is these
// four verbs. Each of them is a state transition, so each has a state in which
// it is refused.
func TestTaskVerbsOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("backlog-agent")
	tasks := "/api/v1/agents/" + agent.ID.String() + "/tasks"

	made := admin.expect(http.MethodPost, tasks,
		map[string]string{"title": "Etwas tun", "description": "und zwar gründlich"}, http.StatusCreated)
	id, _ := made["id"].(string)
	if id == "" {
		t.Fatalf("the task carries no id: %v", made)
	}

	admin.expectList(http.MethodGet, "/api/v1/tasks/"+id+"/transitions", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/tasks/"+id+"/notes", nil, http.StatusOK)

	cancelled := admin.expect(http.MethodPost, "/api/v1/tasks/"+id+"/cancel", nil, http.StatusOK)
	if cancelled["state"] == made["state"] {
		t.Errorf("cancelling left the state at %v", cancelled["state"])
	}

	// The trail is what tells a reader afterwards how a task got where it is.
	// It is written by the transitions, so it exists from the first one on.
	trs := admin.expectList(http.MethodGet, "/api/v1/tasks/"+id+"/transitions", nil, http.StatusOK)
	if len(trs) == 0 {
		t.Error("cancelling left no transition behind — the history would say nothing")
	}
	// And back: a cancelled task is retried rather than created anew, so its
	// history stays in one place.
	admin.expect(http.MethodPost, "/api/v1/tasks/"+id+"/retry", nil, http.StatusOK)

	admin.expect(http.MethodPost, "/api/v1/tasks/"+id+"/cancel", nil, http.StatusOK)
	admin.expect(http.MethodPost, "/api/v1/tasks/"+id+"/archive", nil, http.StatusOK)

	// The sweep takes what is finished out of the board and says how much.
	got := admin.expect(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/backlog/cleanup", nil, http.StatusOK)
	if _, ok := got["archived"]; !ok {
		t.Errorf("the cleanup does not say what it archived: %v", got)
	}

	for _, verb := range []string{"cancel", "retry", "archive"} {
		admin.expect(http.MethodPost, "/api/v1/tasks/keine-uuid/"+verb, nil, http.StatusBadRequest)
		admin.expect(http.MethodPost, "/api/v1/tasks/"+uuid.NewString()+"/"+verb, nil, http.StatusNotFound)
	}
	admin.expect(http.MethodGet, "/api/v1/tasks/keine-uuid/transitions", nil, http.StatusBadRequest)
	admin.expect(http.MethodGet, "/api/v1/tasks/keine-uuid/notes", nil, http.StatusBadRequest)
}

// The board's columns are the agent's own. They name states of work, they can
// be reordered, and one of them cannot be removed while work stands in it.
func TestBoardStagesOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("spalten-agent")
	base := "/api/v1/agents/" + agent.ID.String() + "/stages"

	admin.expectList(http.MethodGet, base, nil, http.StatusOK)

	made := admin.expect(http.MethodPost, base,
		map[string]any{"name": "In Prüfung", "color": "#cc7a5b"}, http.StatusCreated)
	id, _ := made["id"].(string)
	if id == "" {
		t.Fatalf("the column carries no id: %v", made)
	}
	admin.expect(http.MethodPost, base, map[string]any{"name": "Wartet", "color": "#6b675e"}, http.StatusCreated)

	admin.expect(http.MethodPatch, "/api/v1/stages/"+id,
		map[string]any{"name": "Wartet auf Kundschaft", "color": "#123456", "position": 1}, http.StatusOK)
	// A column without a name would be a column nobody can read.
	admin.expect(http.MethodPatch, "/api/v1/stages/"+id, map[string]any{"name": ""}, http.StatusBadRequest)
	admin.expect(http.MethodPatch, "/api/v1/stages/keine-uuid", map[string]any{"name": "X"}, http.StatusBadRequest)

	after := admin.expectList(http.MethodGet, base, nil, http.StatusOK)
	order := make([]string, 0, len(after))
	for _, st := range after {
		if sid, _ := st["id"].(string); sid != "" {
			order = append(order, sid)
		}
	}
	if len(order) < 2 {
		t.Fatalf("only %d columns — there is nothing to reorder", len(order))
	}
	order[0], order[1] = order[1], order[0]
	admin.expect(http.MethodPost, base+"/reorder", map[string]any{"order": order}, http.StatusNoContent)
	// An empty order changes nothing and says so — the board's columns are the
	// agent's own, and "no columns named" is not an error, it is a no-op.
	admin.expect(http.MethodPost, base+"/reorder", map[string]any{"order": []string{}}, http.StatusNoContent)
	admin.expect(http.MethodPost, base+"/reorder", "kein json", http.StatusBadRequest)
	admin.expect(http.MethodPost, "/api/v1/agents/keine-uuid/stages/reorder",
		map[string]any{"order": order}, http.StatusBadRequest)

	admin.expect(http.MethodDelete, "/api/v1/stages/"+id, nil, http.StatusNoContent)
	admin.expect(http.MethodDelete, "/api/v1/stages/keine-uuid", nil, http.StatusBadRequest)
}

// An agent that has been put to sleep by the kill switch comes back by hand,
// and one that is deleted takes its backlog with it.
func TestResumeAndDeleteAgentOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("wieder-wach")
	base := "/api/v1/agents/" + agent.ID.String()

	admin.expect(http.MethodPost, base+"/kill", map[string]any{"killed": true}, http.StatusOK)
	admin.expect(http.MethodPost, base+"/resume", nil, http.StatusOK)
	admin.expect(http.MethodPost, "/api/v1/agents/keine-uuid/resume", nil, http.StatusBadRequest)

	admin.expect(http.MethodDelete, base, nil, http.StatusOK)
	admin.expect(http.MethodGet, base, nil, http.StatusNotFound)
	admin.expect(http.MethodDelete, "/api/v1/agents/keine-uuid", nil, http.StatusBadRequest)
}
