package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"covey/internal/orchestrator"
)

// What a person changes through the API reaches the event stream (#619):
// an agent created and hired, a department added, a task given and
// discarded, an approval decided. Before, none of these published anything,
// and the app showed a new agent only after a restart.
func TestAPIChangesReachTheEventStream(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	events, cancel := s.orch.Events().Subscribe(s.orgID)
	defer cancel()

	// await reads until an event of typ arrives whose data has the given
	// change (empty: any), and fails after a while.
	await := func(typ, change string) orchestrator.Event {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case ev := <-events:
				if ev.Type != typ {
					continue
				}
				d, _ := ev.Data.(map[string]string)
				if change != "" && d["change"] != change {
					continue
				}
				return ev
			case <-deadline:
				t.Fatalf("no %s event (change %q)", typ, change)
				return orchestrator.Event{}
			}
		}
	}

	created := admin.expect(http.MethodPost, "/api/v1/agents",
		map[string]any{"slug": "neu-im-team", "display_name": "Neu", "runtime": "mock"}, http.StatusCreated)
	agentID := created["id"].(string)
	await("roster", orchestrator.RosterCreated)

	admin.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/hire", nil, http.StatusOK)
	if ev := await("roster", orchestrator.RosterHired); ev.AgentID != agentID {
		t.Errorf("the hire names agent %q, want %q", ev.AgentID, agentID)
	}

	admin.expect(http.MethodPatch, "/api/v1/agents/"+agentID+"/name",
		map[string]any{"display_name": "Neu Umbenannt"}, http.StatusOK)
	await("roster", orchestrator.RosterUpdated)

	admin.expect(http.MethodPost, "/api/v1/departments", map[string]any{"name": "Forschung"}, http.StatusCreated)
	await("roster", orchestrator.RosterDepartments)

	// A task on an agent that does not run: the dispatcher publishes nothing
	// for it, so what arrives is the API's own event.
	draft := admin.expect(http.MethodPost, "/api/v1/agents",
		map[string]any{"slug": "entwurf-mit-aufgabe", "display_name": "Entwurf", "runtime": "mock"}, http.StatusCreated)
	draftID := draft["id"].(string)
	await("roster", orchestrator.RosterCreated)
	task := admin.expect(http.MethodPost, "/api/v1/agents/"+draftID+"/tasks",
		map[string]any{"title": "Wartet"}, http.StatusCreated)
	if ev := await("task", ""); ev.AgentID != draftID {
		t.Errorf("the new task names agent %q, want %q", ev.AgentID, draftID)
	}
	admin.expect(http.MethodPost, "/api/v1/tasks/"+task["id"].(string)+"/cancel", nil, http.StatusOK)
	await("task", "")

	agent := s.newSupportAgent("freigabe-agent")
	appr, err := s.obs.CreateApproval(ctx, s.orgID, agent.ID, nil, "http.post", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	admin.expect(http.MethodPost, "/api/v1/approvals/"+appr.ID.String()+"/decide",
		map[string]any{"approve": false}, http.StatusOK)
	if ev := await("approval", ""); ev.Data.(map[string]string)["status"] != "denied" {
		t.Errorf("the decision's event says %+v", ev.Data)
	}
}
