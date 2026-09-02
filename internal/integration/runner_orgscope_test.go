package integration

import (
	"context"
	"net/http"
	"testing"

	runnerstore "covey/internal/runner/store"
)

// The organisation is a property of the runner, not of the request (spec/16,
// "Trust boundary"). Every route under /api/v1/runners/{id}/… has to answer a
// foreign runner's id with "not found" — the update route above all, because
// it hands the host a URL to fetch and run a binary from (#159).
func TestRunnerRoutesDoNotReachIntoAnotherOrganisation(t *testing.T) {
	s := newStack(t)
	c := login(t, s, "admin@test.local", "admin-passwort")
	ctx := context.Background()

	fremdeOrg, _ := nachbar(t, s)
	tokens := runnerstore.NewBuiltinTokens(s.runners)
	fremder, _, err := tokens.For(ctx, fremdeOrg)
	if err != nil {
		t.Fatalf("Runner der Nachbar-AG: %v", err)
	}
	if err := s.runners.PlanUpdate(ctx, fremder, "v9.9.9"); err != nil {
		t.Fatal(err)
	}

	base := "/api/v1/runners/" + fremder.String()
	c.expect(http.MethodPost, base+"/update", map[string]any{"base_url": "https://example.invalid/releases"}, http.StatusNotFound)
	c.expect(http.MethodDelete, base+"/update", nil, http.StatusNotFound)
	c.expect(http.MethodPost, base+"/pull", map[string]any{"workplace": "base"}, http.StatusNotFound)
	c.expect(http.MethodGet, base+"/logs", nil, http.StatusNotFound)
	c.expect(http.MethodPost, base+"/log-level", map[string]any{"level": "debug"}, http.StatusNotFound)

	// And nothing changed over there.
	if planned, err := s.runners.PlannedUpdate(ctx, fremder); err != nil || planned != "v9.9.9" {
		t.Fatalf("the neighbour's plan was touched: %q (%v)", planned, err)
	}
}
