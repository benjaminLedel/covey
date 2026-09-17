package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"covey/internal/agents"
)

// inboxPage is the inbox response, as far as the test reads it.
type inboxPage struct {
	Items []struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		Title   string `json:"title"`
		Status  string `json:"status"`
		Pending bool   `json:"pending"`
	} `json:"items"`
	Total   int `json:"total"`
	Pending int `json:"pending"`
}

func getInbox(t *testing.T, c *apiClient, query string) inboxPage {
	t.Helper()
	resp := c.do(http.MethodGet, "/api/v1/inbox"+query, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /inbox%s: HTTP %d", query, resp.StatusCode)
	}
	var page inboxPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	return page
}

// TestPosteingangSortiertNachDringlichkeit: one list, two kinds — and the
// order carries the difference. An approval halts a task, for an open
// item nobody waits; that is why the approval stands
// on top, even when it came later.
func TestPosteingangSortiertNachDringlichkeit(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	ziel := s.newSupportAgent("kollege")
	autor := s.newSupportAgent("betrieb")

	// First the proposal, then the approval.
	vorschlag(t, s, ziel, autor, map[string]string{"PLAYBOOKS.md": "## Vorgehen\n\nErst lesen."})
	appr, err := s.obs.CreateApproval(ctx, s.orgID, ziel.ID, nil, "zammad:reply_external",
		map[string]any{"ticket": 4711})
	if err != nil {
		t.Fatal(err)
	}

	page := getInbox(t, admin, "?status=open")
	if page.Total != 2 || page.Pending != 2 {
		t.Fatalf("beide Sorten gehoeren in eine Liste: %+v", page)
	}
	if page.Items[0].Type != "approval" || page.Items[0].ID != appr.ID.String() {
		t.Fatalf("die Freigabe gehoert nach oben — dort wartet ein Agent: %+v", page.Items)
	}
	if page.Items[1].Type != "proposal" {
		t.Fatalf("danach der Vorschlag: %+v", page.Items)
	}

	// Filtered by kind, each group returns its own whole set.
	if g := getInbox(t, admin, "?type=approval"); g.Total != 1 || g.Items[0].Type != "approval" {
		t.Fatalf("Gruppe Freigaben: %+v", g)
	}
	if g := getInbox(t, admin, "?type=proposal"); g.Total != 1 || g.Items[0].Type != "proposal" {
		t.Fatalf("Gruppe Vorschlaege: %+v", g)
	}

	// Paging is server-side: one page, and the number beside it stays the whole
	// set — otherwise "load more" would not know that something is left.
	seite := getInbox(t, admin, "?limit=1")
	if len(seite.Items) != 1 || seite.Total != 2 {
		t.Fatalf("limit schneidet die Seite, nicht den Bestand: %+v", seite)
	}
	zweite := getInbox(t, admin, "?limit=1&offset=1")
	if len(zweite.Items) != 1 || zweite.Items[0].ID == seite.Items[0].ID {
		t.Fatalf("offset muss weiterblaettern: %+v", zweite)
	}
}

// TestPosteingangControllingSiehtKeineArbeitsakte: Controlling may read approvals
// (it always could) but not the assessment of a colleague — a
// cost sheet says what was spent, a proposal says how someone
// worked (spec/21).
func TestPosteingangControllingSiehtKeineArbeitsakte(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	s.mitglied(t, "controlling@test.local", "Controlling", "controlling", "controlling-passwort")
	admin := login(t, s, "admin@test.local", "admin-passwort")
	controlling := login(t, s, "controlling@test.local", "controlling-passwort")
	ziel := s.newSupportAgent("kollege")
	autor := s.newSupportAgent("betrieb")

	vorschlag(t, s, ziel, autor, map[string]string{"PLAYBOOKS.md": "## Vorgehen\n\nErst lesen."})
	if _, err := s.obs.CreateApproval(ctx, s.orgID, ziel.ID, nil, "zammad:reply_external", map[string]any{}); err != nil {
		t.Fatal(err)
	}

	if page := getInbox(t, admin, ""); page.Total != 2 {
		t.Fatalf("der Administrator sieht beides: %+v", page)
	}
	page := getInbox(t, controlling, "")
	if page.Total != 1 || page.Items[0].Type != "approval" {
		t.Fatalf("Controlling sieht nur die Freigabe: %+v", page)
	}
	// Nor over the direct route.
	controlling.expect(http.MethodGet, "/api/v1/improvements", nil, http.StatusForbidden)
}

// TestPosteingangEntscheidungVerschwindetAusDemVorrat: decided means off
// the pile and still findable — the listing by kind is the
// archive, the pile on top is not.
func TestPosteingangEntscheidungVerschwindetAusDemVorrat(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	ziel := s.newSupportAgent("kollege")
	autor := s.newSupportAgent("betrieb")
	item := vorschlag(t, s, ziel, autor, map[string]string{"PLAYBOOKS.md": "## Vorgehen\n\nErst lesen."})

	admin.expect(http.MethodPost, "/api/v1/improvements/"+item.ID.String()+"/decide",
		map[string]any{"accept": false, "note": "Nicht noetig."}, http.StatusOK)

	if offen := getInbox(t, admin, "?status=open"); offen.Total != 0 || offen.Pending != 0 {
		t.Fatalf("entschieden gehoert nicht mehr in den Vorrat: %+v", offen)
	}
	entschieden := getInbox(t, admin, "?status=decided")
	if entschieden.Total != 1 || entschieden.Items[0].Status != string(agents.ImprovementRejected) {
		t.Fatalf("und bleibt in der Auflistung stehen: %+v", entschieden)
	}
}
