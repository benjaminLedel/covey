package integration

import (
	"context"
	"net/http"
	"testing"

	"covey/internal/egress"
)

// An organisation can open egress for its agents (#587): the effective
// allowlist becomes MatchAll, and closing it again restores the list. Only the
// security roles may flip it.
func TestEgressOpenPerOrganisation(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("offen-1")
	if _, err := s.egress.AddAgentHost(ctx, agent.ID, "docs.example.org", ""); err != nil {
		t.Fatal(err)
	}

	st := admin.expect(http.MethodGet, "/api/v1/egress", nil, http.StatusOK)
	if st["open"] != false {
		t.Fatalf("egress has to start restricted: %v", st)
	}

	s.mitglied(t, "ada@test.local", "Ada", "agent_owner", "ada-passwort")
	ada := login(t, s, "ada@test.local", "ada-passwort")
	ada.expect(http.MethodPatch, "/api/v1/egress/open", map[string]bool{"open": true}, http.StatusForbidden)
	admin.expect(http.MethodPatch, "/api/v1/egress/open", map[string]any{}, http.StatusBadRequest)

	admin.expect(http.MethodPatch, "/api/v1/egress/open", map[string]bool{"open": true}, http.StatusOK)
	if st := admin.expect(http.MethodGet, "/api/v1/egress", nil, http.StatusOK); st["open"] != true {
		t.Fatalf("the status does not report the open organisation: %v", st)
	}
	got, err := s.egress.EffectiveAllowlist(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != egress.MatchAll {
		t.Fatalf("an open organisation's allowlist has to be MatchAll alone: %v", got)
	}

	admin.expect(http.MethodPatch, "/api/v1/egress/open", map[string]bool{"open": false}, http.StatusOK)
	got, err = s.egress.EffectiveAllowlist(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range got {
		if p == egress.MatchAll {
			t.Fatalf("closing egress has to remove MatchAll: %v", got)
		}
	}
}
