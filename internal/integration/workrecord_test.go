package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/backlog"
)

// TestArbeitsakteZaehltWasPassiertIst: the record is a query over what the
// control plane wrote down itself — not over what an agent reports. The test
// lets real runs run and checks that every section comes from its named
// source (spec/21).
func TestArbeitsakteZaehltWasPassiertIst(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("kollege")

	// One indicator, so the section has something to show.
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md":   "# Support-Agent\n\n## Rolle\nSupport.",
		"ACCESS.md": "- system: zammad scope: read,write",
		"KPIS.md":   "- kennzahl: erledigte-aufgaben titel: Erledigte Aufgaben zählt: aufgabe erledigt ziel: 5 pro woche",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}

	// One run that goes through, and one that ends at the turn limit.
	erledigt, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Geht durch", "[mock:result Fertig.]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	amLimit, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Endet am Limit",
		"[mock:maxturns-always Haelfte erledigt, Rest offen.]", "heartbeat", 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{erledigt.ID, amLimit.ID} {
		taskID := id
		waitFor(t, "the run finishes", 40*time.Second, func() bool {
			st := s.taskState(taskID)
			return st == backlog.StateDone || st == backlog.StateFailed
		})
	}

	var rec map[string]any
	resp := admin.do(http.MethodGet, "/api/v1/agents/"+agent.ID.String()+"/work-record?days=30", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&rec); err != nil {
		t.Fatal(err)
	}

	// Throughput: by state and by origin, and the task rows with their titles
	// — the one honest exception the record makes.
	tp := rec["throughput"].(map[string]any)
	if len(tp["by_state"].([]any)) == 0 || len(tp["by_origin"].([]any)) == 0 {
		t.Fatalf("Durchsatz nach Zustand und Herkunft erwartet: %v", tp)
	}
	// Both tasks stand there — the run at the limit several times, because
	// every continuation is its own task. That is what should be visible:
	// three rows with the same title are the finding.
	gesehen := map[string]int{}
	for _, raw := range tp["tasks"].([]any) {
		gesehen[raw.(map[string]any)["title"].(string)]++
	}
	if gesehen["Geht durch"] != 1 || gesehen["Endet am Limit"] < 1 {
		t.Fatalf("beide Aufgaben gehoeren in die Akte: %v", gesehen)
	}

	// Aborts: the run at the turn limit stands with its reason, and not as an
	// anonymous failure — that is the finding at issue.
	var amLimitGezaehlt bool
	for _, raw := range rec["aborts"].([]any) {
		c := raw.(map[string]any)
		if c["key"] == "max_turns" && c["count"].(float64) > 0 {
			amLimitGezaehlt = true
		}
	}
	if !amLimitGezaehlt {
		t.Fatalf("der Lauf am Turn-Limit muss als max_turns gezaehlt sein: %v", rec["aborts"])
	}

	// Cost: it comes from cost_entries and not from a report.
	cost := rec["cost"].(map[string]any)
	if cost["total_usd"].(float64) <= 0 || cost["tasks"].(float64) <= 0 {
		t.Fatalf("die Laeufe haben etwas gekostet: %v", cost)
	}

	// Indicators: the agent's own counting rule, with its goal.
	inds := rec["indicators"].([]any)
	if len(inds) != 1 || inds[0].(map[string]any)["goal"].(float64) != 5 {
		t.Fatalf("die Kennzahl aus der KPIS.md mit ihrem Ziel erwartet: %v", inds)
	}
}

// TestArbeitsakteZeigtHaengendeAufgaben: the failure shape nobody sees
// because nothing fails — deliberately without a time window, for a task that
// has waited for months is exactly the finding a period would hide.
func TestArbeitsakteZeigtHaengendeAufgaben(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("kollege")

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Wartet auf Antwort",
		"[mock:block key=zammad:ticket:42 question=Kommt der Kunde zurueck?]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the task blocks", 40*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateBlocked
	})

	rec := admin.expect(http.MethodGet,
		"/api/v1/agents/"+agent.ID.String()+"/work-record?days=1", nil, http.StatusOK)

	stuck := rec["stuck"].([]any)
	if len(stuck) != 1 {
		t.Fatalf("die haengende Aufgabe gehoert in die Akte: %v", stuck)
	}
	if stuck[0].(map[string]any)["correlation_key"] != "zammad:ticket:42" {
		t.Fatalf("worauf sie wartet, gehoert dazu: %v", stuck[0])
	}
}

// TestArbeitsakteFolgtDenRecordings: who may read it is decided here and not
// inherited. Controlling may see cost totals and not the record — "whoever
// may see the bill may also see this" would be the answer that makes the
// function unusable in any firm with a works council (spec/21).
func TestArbeitsakteFolgtDenRecordings(t *testing.T) {
	s := newStack(t)
	for _, rolle := range []string{"controlling", "auditor"} {
		s.mitglied(t, rolle+"@test.local", rolle, rolle, rolle+"-passwort")
	}
	agent := s.newSupportAgent("kollege")
	pfad := "/api/v1/agents/" + agent.ID.String() + "/work-record"

	login(t, s, "admin@test.local", "admin-passwort").expect(http.MethodGet, pfad, nil, http.StatusOK)
	login(t, s, "auditor@test.local", "auditor-passwort").expect(http.MethodGet, pfad, nil, http.StatusOK)
	login(t, s, "controlling@test.local", "controlling-passwort").
		expect(http.MethodGet, pfad, nil, http.StatusForbidden)

	// And the indicators of ONE agent inherit the same boundary: reading
	// them from the record is the same act as reading the record. The
	// org-wide price list stays open — it groups over indicators, not over
	// persons.
	login(t, s, "controlling@test.local", "controlling-passwort").
		expect(http.MethodGet, "/api/v1/agents/"+agent.ID.String()+"/cost/indicators", nil, http.StatusForbidden)
	login(t, s, "controlling@test.local", "controlling-passwort").
		expect(http.MethodGet, "/api/v1/cost/indicators", nil, http.StatusOK)
}

// TestArbeitsakteZaehltEigeneVorschlaege: whoever wants to check covey Doctor
// reads its rejection rate — and that stands in its own record as it does for
// anyone else (spec/21, "who checks the checker").
func TestArbeitsakteZaehltEigeneVorschlaege(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	autor := s.newSupportAgent("betrieb")
	ziel := s.newSupportAgent("kollege")

	angenommen := vorschlag(t, s, ziel, autor, map[string]string{"PLAYBOOKS.md": "## Vorgehen\n\nErst lesen."})
	abgelehnt, err := s.registry.CreateImprovement(ctx, agents.ImprovementItem{
		OrgID: s.orgID, AgentID: ziel.ID, Kind: agents.KindProposal,
		Title: "Zu weit gegriffen", Files: map[string]string{"SOUL.md": "# Anders"},
		AuthorAgentID: &autor.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	admin.expect(http.MethodPost, "/api/v1/improvements/"+angenommen.ID.String()+"/decide",
		map[string]any{"accept": true}, http.StatusOK)
	admin.expect(http.MethodPost, "/api/v1/improvements/"+abgelehnt.ID.String()+"/decide",
		map[string]any{"accept": false}, http.StatusOK)

	var rec map[string]any
	resp := admin.do(http.MethodGet, "/api/v1/agents/"+autor.ID.String()+"/work-record", nil)
	defer resp.Body.Close()
	json.NewDecoder(resp.Body).Decode(&rec)

	gezaehlt := map[string]float64{}
	for _, raw := range rec["friction"].(map[string]any)["proposals"].([]any) {
		c := raw.(map[string]any)
		gezaehlt[c["key"].(string)] = c["count"].(float64)
	}
	if gezaehlt["accepted"] != 1 || gezaehlt["rejected"] != 1 {
		t.Fatalf("die eigenen Vorschlaege gehoeren in die Akte ihres Absenders: %v", gezaehlt)
	}

	// And at the colleague being RATED they do not stand as his own.
	resp2 := admin.do(http.MethodGet, "/api/v1/agents/"+ziel.ID.String()+"/work-record", nil)
	defer resp2.Body.Close()
	var beimZiel map[string]any
	json.NewDecoder(resp2.Body).Decode(&beimZiel)
	if len(beimZiel["friction"].(map[string]any)["proposals"].([]any)) != 0 {
		t.Fatal("ein Vorschlag UEBER jemanden ist kein Vorschlag VON ihm")
	}
}
