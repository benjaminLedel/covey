package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"covey/internal/agents"
)

// A finding is the kind of improvement that carries no diff: it is a sentence,
// and it stays open until a person closes it. Whatever files are handed in with
// one are dropped — otherwise a "finding" would be a proposal that skipped the
// review the proposal kind exists for.
func TestAFindingCarriesNoDiffWhateverIsHandedIn(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("befund-agent")

	item, err := s.registry.CreateImprovement(ctx, agents.ImprovementItem{
		OrgID: s.orgID, AgentID: agent.ID, Kind: agents.KindFinding,
		Title:     "Das Ticketsystem antwortet langsam",
		Rationale: "Drei Läufe liefen in den Zeitablauf.",
		Files:     map[string]string{"ACCESS.md": "- system: alles scope: alles"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(item.Files) != 0 {
		t.Errorf("the finding kept a diff: %v", item.Files)
	}

	// Declining is a decision too, and the reason is recorded — a finding that
	// vanished without one would come back next week as the same finding.
	admin.expect(http.MethodPost, "/api/v1/improvements/"+item.ID.String()+"/decide",
		map[string]any{"accept": false, "note": "bekannt, wird beobachtet"}, http.StatusOK)
	after := admin.expect(http.MethodGet, "/api/v1/improvements/"+item.ID.String(), nil, http.StatusOK)
	if after["status"] == "pending" {
		t.Error("declining left the item open")
	}
	if after["decision_note"] != "bekannt, wird beobachtet" {
		t.Errorf("the reason was not recorded: %v", after["decision_note"])
	}

	// And only once. Two people deciding at the same moment produce one
	// decision and one error, not two.
	admin.expect(http.MethodPost, "/api/v1/improvements/"+item.ID.String()+"/decide",
		map[string]any{"accept": false}, http.StatusConflict)
}

// A proposal without a diff is not a proposal. Storing one would produce an
// item nobody can accept, sitting in the list forever.
func TestAnEmptyProposalIsRefused(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("leer-agent")

	if _, err := s.registry.CreateImprovement(ctx, agents.ImprovementItem{
		OrgID: s.orgID, AgentID: agent.ID, Kind: agents.KindProposal,
		Title: "Ohne Inhalt",
	}); err == nil {
		t.Fatal("a proposal without files was stored")
	}

	// And an item for an agent of another organisation is not found: the item
	// belongs to the agent, and the agent to one organisation.
	if _, err := s.registry.CreateImprovement(ctx, agents.ImprovementItem{
		OrgID: uuid.New(), AgentID: agent.ID, Kind: agents.KindFinding, Title: "Fremd",
	}); err == nil {
		t.Error("an item was created across organisations")
	}
}

// The list is filtered by status and by kind, because the two questions people
// actually ask are "what is still open" and "what did the agents propose".
func TestImprovementListFilters(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("filter-agent")

	proposal, err := s.registry.CreateImprovement(ctx, agents.ImprovementItem{
		OrgID: s.orgID, AgentID: agent.ID, Kind: agents.KindProposal,
		Title: "Ein Vorschlag", Files: map[string]string{"SOUL.md": "# Neu\n\n## Role\nNeu."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.CreateImprovement(ctx, agents.ImprovementItem{
		OrgID: s.orgID, AgentID: agent.ID, Kind: agents.KindFinding, Title: "Ein Befund",
	}); err != nil {
		t.Fatal(err)
	}

	all := admin.expectList(http.MethodGet, "/api/v1/improvements", nil, http.StatusOK)
	if len(all) < 2 {
		t.Fatalf("the list holds %d items", len(all))
	}
	proposals := admin.expectList(http.MethodGet, "/api/v1/improvements?kind=proposal", nil, http.StatusOK)
	for _, p := range proposals {
		if p["kind"] != "proposal" {
			t.Errorf("the kind filter let a %v through", p["kind"])
		}
	}

	admin.expect(http.MethodPost, "/api/v1/improvements/"+proposal.ID.String()+"/decide",
		map[string]any{"accept": true}, http.StatusOK)
	open := admin.expectList(http.MethodGet, "/api/v1/improvements?status=pending", nil, http.StatusOK)
	for _, p := range open {
		if p["status"] != "pending" {
			t.Errorf("the status filter let a %v through", p["status"])
		}
		if p["id"] == proposal.ID.String() {
			t.Error("a decided item is still listed as open")
		}
	}
}
