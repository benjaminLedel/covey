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

// TestArbeitsakteZaehltWasPassiertIst: die Akte ist eine Abfrage über das, was
// die Control Plane selbst aufgeschrieben hat — nicht über das, was ein Agent
// berichtet. Der Test lässt echte Läufe laufen und prüft, dass jeder Abschnitt
// aus seiner benannten Quelle kommt (spec/21).
func TestArbeitsakteZaehltWasPassiertIst(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("kollege")

	// Eine Kennzahl, damit der Abschnitt etwas zu zeigen hat.
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md":   "# Support-Agent\n\n## Rolle\nSupport.",
		"ACCESS.md": "- system: zammad scope: read,write",
		"KPIS.md":   "- kennzahl: erledigte-aufgaben titel: Erledigte Aufgaben zählt: aufgabe erledigt ziel: 5 pro woche",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}

	// Ein Lauf, der durchgeht, und einer, der am Turn-Limit endet.
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

	// Durchsatz: nach Zustand und nach Herkunft, und die Aufgabenzeilen mit
	// ihren Titeln — die eine ehrliche Ausnahme der Akte.
	tp := rec["throughput"].(map[string]any)
	if len(tp["by_state"].([]any)) == 0 || len(tp["by_origin"].([]any)) == 0 {
		t.Fatalf("Durchsatz nach Zustand und Herkunft erwartet: %v", tp)
	}
	// Beide Aufgaben stehen da — der Lauf am Limit mehrfach, weil jede
	// Fortsetzung eine eigene Aufgabe ist. Genau das soll sichtbar sein: drei
	// Zeilen mit demselben Titel sind der Befund.
	gesehen := map[string]int{}
	for _, raw := range tp["tasks"].([]any) {
		gesehen[raw.(map[string]any)["title"].(string)]++
	}
	if gesehen["Geht durch"] != 1 || gesehen["Endet am Limit"] < 1 {
		t.Fatalf("beide Aufgaben gehoeren in die Akte: %v", gesehen)
	}

	// Abbrüche: der Lauf am Turn-Limit steht mit seinem Grund da, und nicht
	// als anonymer Fehlschlag — das ist der Befund, um den es geht.
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

	// Kosten: sie kommen aus cost_entries und nicht aus einer Meldung.
	cost := rec["cost"].(map[string]any)
	if cost["total_usd"].(float64) <= 0 || cost["tasks"].(float64) <= 0 {
		t.Fatalf("die Laeufe haben etwas gekostet: %v", cost)
	}

	// Kennzahlen: die eigene Zählregel des Agenten, mit ihrem Ziel.
	inds := rec["indicators"].([]any)
	if len(inds) != 1 || inds[0].(map[string]any)["goal"].(float64) != 5 {
		t.Fatalf("die Kennzahl aus der KPIS.md mit ihrem Ziel erwartet: %v", inds)
	}
}

// TestArbeitsakteZeigtHaengendeAufgaben: die Fehlerform, die niemand sieht,
// weil nichts fehlschlägt — bewusst ohne Zeitfenster, denn eine Aufgabe, die
// seit Monaten wartet, ist genau der Befund, den ein Zeitraum verstecken würde.
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

// TestArbeitsakteFolgtDenRecordings: wer sie lesen darf, ist hier entschieden
// und nicht geerbt. Controlling darf Kostensummen sehen und die Akte nicht —
// „wer die Rechnung sehen darf, darf auch das sehen" wäre die Antwort, die die
// Funktion in jedem Betrieb mit Betriebsrat unbenutzbar macht (spec/21).
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

	// Und die Kennzahlen EINES Agenten erben dieselbe Grenze: sie aus der Akte
	// zu lesen ist dieselbe Handlung wie die Akte zu lesen. Die org-weite
	// Preisliste bleibt offen — sie gruppiert ueber Kennzahlen, nicht ueber
	// Personen.
	login(t, s, "controlling@test.local", "controlling-passwort").
		expect(http.MethodGet, "/api/v1/agents/"+agent.ID.String()+"/cost/indicators", nil, http.StatusForbidden)
	login(t, s, "controlling@test.local", "controlling-passwort").
		expect(http.MethodGet, "/api/v1/cost/indicators", nil, http.StatusOK)
}

// TestArbeitsakteZaehltEigeneVorschlaege: wer covey Doctor überprüfen
// will, liest seine Ablehnungsquote — und die steht in seiner eigenen Akte wie
// bei jedem anderen auch (spec/21, „wer prüft den Prüfer").
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

	// Und beim BEWERTETEN Kollegen stehen sie nicht als seine eigenen.
	resp2 := admin.do(http.MethodGet, "/api/v1/agents/"+ziel.ID.String()+"/work-record", nil)
	defer resp2.Body.Close()
	var beimZiel map[string]any
	json.NewDecoder(resp2.Body).Decode(&beimZiel)
	if len(beimZiel["friction"].(map[string]any)["proposals"].([]any)) != 0 {
		t.Fatal("ein Vorschlag UEBER jemanden ist kein Vorschlag VON ihm")
	}
}
