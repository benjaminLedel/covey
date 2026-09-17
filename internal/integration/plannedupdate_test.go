package integration

import (
	"context"
	"net/http"
	"testing"

	runnerstore "covey/internal/runner/store"
)

// An update is refused as long as the host carries sandboxes — and up to here
// the waiting stayed with the human: press, refused, try again later. On
// a production instance that cost two hours, while precisely
// the agent whose sandbox blocked suffered from the fault that the update
// fixes.
//
// The wish therefore belongs on the row of the runner, not in the head of the
// operator — and it has to survive a restart of the control plane.
func TestEinGeplantesUpdateStehtAnDerRunnerZeile(t *testing.T) {
	s := newStack(t)
	c := login(t, s, "admin@test.local", "admin-passwort")
	ctx := context.Background()

	tokens := runnerstore.NewBuiltinTokens(s.runners)
	runnerID, _, err := tokens.For(ctx, s.orgID)
	if err != nil {
		t.Fatalf("Runner: %v", err)
	}

	// Note it down, as the control plane does when a host is busy.
	if err := s.runners.PlanUpdate(ctx, runnerID, "v9.9.9"); err != nil {
		t.Fatalf("PlanUpdate: %v", err)
	}
	geplant, err := s.runners.PlannedUpdate(ctx, runnerID)
	if err != nil || geplant != "v9.9.9" {
		t.Fatalf("der Plan wurde nicht gespeichert: %q (%v)", geplant, err)
	}

	// And it is visible where the runner is managed.
	liste := c.expectList(http.MethodGet, "/api/v1/runners", nil, http.StatusOK)
	var gefunden bool
	for _, r := range liste {
		if r["id"] == runnerID.String() {
			gefunden = true
			if r["update_to"] != "v9.9.9" {
				t.Errorf("die Runner-Ansicht zeigt den Plan nicht: %v", r["update_to"])
			}
			if r["update_planned_at"] == nil {
				t.Error("seit wann geplant wird, gehört dazu")
			}
		}
	}
	if !gefunden {
		t.Fatal("der Runner steht nicht in der Liste")
	}

	// It has to be withdrawable too — over its own route, because an
	// empty version on an update means "the newest" and one field cannot
	// mean both.
	c.expect(http.MethodDelete, "/api/v1/runners/"+runnerID.String()+"/update", nil, http.StatusOK)
	geplant, err = s.runners.PlannedUpdate(ctx, runnerID)
	if err != nil || geplant != "" {
		t.Fatalf("der Plan wurde nicht zurückgenommen: %q (%v)", geplant, err)
	}
}
