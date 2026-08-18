package integration

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"covey/internal/backlog"
)

// TestBacklogSearchFindsWhatTheBoardHides checks the point of the backlog
// search: it has to find precisely the tasks that are no longer standing on the
// board. An archived task is invisible in the normal list — a search that only
// looked at the active board would return what one could already see and would
// be furniture. The wildcard case belongs with it: whoever types a '%' into the
// field is searching for a character, not asking for the whole backlog.
func TestBacklogSearchFindsWhatTheBoardHides(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	// Paused: the tasks are meant to stay put, not to be dispatched.
	agent := s.newSupportAgent("search-agent")
	if err := s.registry.SetKilled(ctx, agent.ID, true); err != nil {
		t.Fatal(err)
	}

	// One old, finished and archived task — the case the search exists for.
	alt := s.terminalTaskInStage(t, agent.ID, "Rechnung 2023 storniert", "Abrechnung")
	if _, err := s.backlog.Archive(ctx, alt.ID); err != nil {
		t.Fatal(err)
	}
	// Two on the active board: one that matches, one that does not.
	treffer, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Rechnung prüfen", "", "manual", 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Urlaubsantrag", "", "manual", 5); err != nil {
		t.Fatal(err)
	}

	admin := login(t, s, "admin@test.local", "admin-passwort")
	base := "/api/v1/agents/" + agent.ID.String() + "/backlog"
	search := func(q string) []map[string]any {
		t.Helper()
		return admin.expectList(http.MethodGet, base+"?q="+url.QueryEscape(q), nil, http.StatusOK)
	}
	titles := func(list []map[string]any) map[string]bool {
		out := map[string]bool{}
		for _, e := range list {
			out[e["title"].(string)] = true
		}
		return out
	}

	// The board without a search: the archived task is not part of it.
	if got := titles(admin.expectList(http.MethodGet, base, nil, http.StatusOK)); got["Rechnung 2023 storniert"] {
		t.Fatalf("the archived task has no business on the active board: %v", got)
	}

	got := titles(search("rechnung"))
	if !got["Rechnung 2023 storniert"] {
		t.Fatalf("the search must reach into the archive — that is what it is for; found: %v", got)
	}
	if !got["Rechnung prüfen"] {
		t.Fatalf("the search must also find the active task; found: %v", got)
	}
	if got["Urlaubsantrag"] {
		t.Fatalf("the search returned something that does not match: %v", got)
	}

	// The body is searched along with it — a task is often only found by its
	// description, because the title is a headline.
	claimed, err := s.backlog.ClaimNext(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != treffer.ID {
		t.Fatalf("expected the older open task to be claimed, got %q", claimed.Title)
	}
	if _, err := s.backlog.Complete(ctx, treffer.ID, backlog.StateDone, "Kundennummer 4711 geklärt", ""); err != nil {
		t.Fatal(err)
	}
	if got := titles(search("4711")); !got["Rechnung prüfen"] {
		t.Fatalf("what a run left behind has to be findable too; found: %v", got)
	}

	// A wildcard is a character, not a query. Without escaping this would return
	// every task of the agent.
	if got := search("%"); len(got) != 0 {
		t.Fatalf("'%%' must be searched as text, got %d hits", len(got))
	}
}
