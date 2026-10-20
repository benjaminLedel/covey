package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/identity"
	"covey/internal/orchestrator"
)

// A roster change through the API publishes a `roster` event (#619) — after
// a successful answer only, to the caller's organisation, naming the agent
// where the route carries it.
func TestRosterPublishesAfterSuccess(t *testing.T) {
	org := uuid.New()
	agent := uuid.New()
	s := &Server{Orch: orchestrator.New(orchestrator.Options{})}
	events, cancel := s.Orch.Events().Subscribe(org, "roster")
	defer cancel()

	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, map[string]bool{"ok": true}) }
	refused := func(w http.ResponseWriter, r *http.Request) { writeErr(w, http.StatusConflict, "no") }
	mux.Handle("PATCH /agents/{id}/name", s.roster(orchestrator.RosterUpdated, ok))
	mux.Handle("POST /agents", s.roster(orchestrator.RosterCreated, ok))
	mux.Handle("POST /templates/{id}/instantiate", s.roster(orchestrator.RosterCreated, ok))
	mux.Handle("DELETE /agents/{id}", s.roster(orchestrator.RosterDeleted, refused))

	do := func(method, path string) {
		t.Helper()
		r := httptest.NewRequest(method, path, nil)
		r = r.WithContext(context.WithValue(r.Context(), principalKey, identity.Principal{OrgID: org}))
		mux.ServeHTTP(httptest.NewRecorder(), r)
	}

	do(http.MethodPatch, "/agents/"+agent.String()+"/name")
	ev := nextLiveEvent(t, events)
	if ev.AgentID != agent.String() || ev.Data.(map[string]string)["change"] != "updated" {
		t.Fatalf("rename published %+v", ev)
	}

	do(http.MethodPost, "/agents")
	if ev := nextLiveEvent(t, events); ev.AgentID != "" || ev.Data.(map[string]string)["change"] != "created" {
		t.Fatalf("create published %+v", ev)
	}

	// The {id} of an instantiation is the template's, not an agent's.
	do(http.MethodPost, "/templates/"+uuid.NewString()+"/instantiate")
	if ev := nextLiveEvent(t, events); ev.AgentID != "" {
		t.Fatalf("instantiation named the template as the agent: %+v", ev)
	}

	do(http.MethodDelete, "/agents/"+agent.String())
	select {
	case ev := <-events:
		t.Fatalf("a refused change published %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func nextLiveEvent(t *testing.T, ch chan orchestrator.Event) orchestrator.Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("no event")
		return orchestrator.Event{}
	}
}
