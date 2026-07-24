package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"covey/internal/backlog"
)

// TestOrgChart deckt das Organigramm ab: Vorgesetzten-Beziehungen für Menschen
// (manager_id, zyklusfrei) und Agenten (supervisor_id), die für alle Rollen
// lesbare Chart-Sicht und das Org-Scoping des Vorgesetzten.
func TestOrgChart(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// Lead anlegen, der an den Admin berichtet.
	lead := admin.expect(http.MethodPost, "/api/v1/users", map[string]string{
		"email": "lead@test.local", "display_name": "Lead", "role": "agent_owner",
		"password": "lead-passwort",
	}, http.StatusCreated)
	leadID := lead["id"].(string)
	admin.expect(http.MethodPatch, "/api/v1/users/"+leadID,
		map[string]string{"manager_id": s.adminID.String()}, http.StatusOK)

	// Zyklen sind verboten: weder Selbstbezug noch Admin → Lead → Admin.
	admin.expect(http.MethodPatch, "/api/v1/users/"+leadID,
		map[string]string{"manager_id": leadID}, http.StatusConflict)
	admin.expect(http.MethodPatch, "/api/v1/users/"+s.adminID.String(),
		map[string]string{"manager_id": leadID}, http.StatusConflict)

	// Der Org-Chart-Endpunkt (Drag & Drop im UI) kann Menschen ebenso umhängen:
	// Zyklen werden abgewiesen, leere manager_id löst die Zuordnung.
	admin.expect(http.MethodPatch, "/api/v1/org/humans/"+leadID+"/manager",
		map[string]string{"manager_id": leadID}, http.StatusConflict)
	admin.expect(http.MethodPatch, "/api/v1/org/humans/"+leadID+"/manager",
		map[string]string{"manager_id": ""}, http.StatusOK)
	if h := admin.expect(http.MethodGet, "/api/v1/org/humans/"+leadID, nil, http.StatusOK); h["manager_id"] != nil {
		t.Fatalf("manager_id muss gelöst sein, got %v", h["manager_id"])
	}
	admin.expect(http.MethodPatch, "/api/v1/org/humans/"+leadID+"/manager",
		map[string]string{"manager_id": s.adminID.String()}, http.StatusOK)

	// Agent anlegen und dem Lead zuordnen.
	created := admin.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "nova", "display_name": "Nova"}, http.StatusCreated)
	agentID := created["id"].(string)
	admin.expect(http.MethodPatch, "/api/v1/agents/"+agentID+"/supervisor",
		map[string]string{"supervisor_id": leadID}, http.StatusOK)

	// Der Chart ist für jede Rolle lesbar und enthält beide Beziehungen.
	viewer := login(t, s, "lead@test.local", "lead-passwort")
	resp := viewer.do(http.MethodGet, "/api/v1/org/chart", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("org/chart erwartet 200, got %d", resp.StatusCode)
	}
	var chart struct {
		Humans []map[string]any `json:"humans"`
		Agents []map[string]any `json:"agents"`
	}
	json.NewDecoder(resp.Body).Decode(&chart)
	resp.Body.Close()
	if len(chart.Humans) != 2 || len(chart.Agents) != 1 {
		t.Fatalf("erwartet 2 menschen / 1 agent, got %d/%d", len(chart.Humans), len(chart.Agents))
	}
	for _, h := range chart.Humans {
		if h["email"] == "lead@test.local" && h["manager_id"] != s.adminID.String() {
			t.Fatalf("lead muss an den admin berichten, got %v", h["manager_id"])
		}
	}
	if chart.Agents[0]["supervisor_id"] != leadID {
		t.Fatalf("agent muss an den lead berichten, got %v", chart.Agents[0]["supervisor_id"])
	}

	// Leere supervisor_id löst die Zuordnung.
	admin.expect(http.MethodPatch, "/api/v1/agents/"+agentID+"/supervisor",
		map[string]string{"supervisor_id": ""}, http.StatusOK)
	got := admin.expect(http.MethodGet, "/api/v1/agents/"+agentID, nil, http.StatusOK)
	if _, hasSupervisor := got["supervisor_id"]; hasSupervisor {
		t.Fatalf("supervisor_id muss gelöst sein, got %v", got["supervisor_id"])
	}

	// Abteilungen können ein oder mehrere Leitungen haben — Menschen wie
	// Agenten. Zuweisen ist idempotent, die Leitungen stehen in der
	// Abteilungs-Antwort, Entfernen löscht genau die eine Zuordnung.
	dept := admin.expect(http.MethodPost, "/api/v1/departments",
		map[string]string{"name": "Support", "color": "#7d9471"}, http.StatusCreated)
	deptID := dept["id"].(string)
	if dept["color"] != "#7d9471" {
		t.Fatalf("abteilung muss die farbe tragen, got %v", dept["color"])
	}

	// Farbe ändern und zurücksetzen; kaputte Werte werden abgewiesen.
	admin.expect(http.MethodPatch, "/api/v1/departments/"+deptID+"/color",
		map[string]string{"color": "#c9a227"}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/departments/"+deptID+"/color",
		map[string]string{"color": "rot"}, http.StatusBadRequest)
	admin.expect(http.MethodPatch, "/api/v1/departments/"+deptID+"/color",
		map[string]string{"color": ""}, http.StatusOK)
	admin.expect(http.MethodPost, "/api/v1/departments/"+deptID+"/leads",
		map[string]string{"kind": "human", "member_id": leadID}, http.StatusOK)
	admin.expect(http.MethodPost, "/api/v1/departments/"+deptID+"/leads",
		map[string]string{"kind": "human", "member_id": leadID}, http.StatusOK)
	admin.expect(http.MethodPost, "/api/v1/departments/"+deptID+"/leads",
		map[string]string{"kind": "agent", "member_id": agentID}, http.StatusOK)

	deptsResp := admin.do(http.MethodGet, "/api/v1/departments", nil)
	var depts []struct {
		Leads []map[string]any `json:"leads"`
	}
	json.NewDecoder(deptsResp.Body).Decode(&depts)
	deptsResp.Body.Close()
	if len(depts) != 1 || len(depts[0].Leads) != 2 {
		t.Fatalf("erwartet 1 abteilung mit 2 leitungen, got %+v", depts)
	}

	admin.expect(http.MethodDelete, "/api/v1/departments/"+deptID+"/leads/"+agentID, nil, http.StatusOK)
	admin.expect(http.MethodDelete, "/api/v1/departments/"+deptID+"/leads/"+agentID, nil, http.StatusNotFound)

	// Org-Scoping: ein Mensch einer fremden Organisation ist kein gültiger
	// Vorgesetzter — weder für Agenten noch für Menschen.
	admin.expect(http.MethodPost, "/api/v1/orgs", map[string]string{
		"name": "Fremde Org", "admin_email": "chef@fremd.local", "admin_name": "Chef",
		"admin_password": "chef-passwort",
	}, http.StatusCreated)
	fremd := login(t, s, "chef@fremd.local", "chef-passwort")
	fremdUsers := fremd.do(http.MethodGet, "/api/v1/users", nil)
	var fu []map[string]any
	json.NewDecoder(fremdUsers.Body).Decode(&fu)
	fremdUsers.Body.Close()
	fremdID := fu[0]["id"].(string)

	admin.expect(http.MethodPatch, "/api/v1/agents/"+agentID+"/supervisor",
		map[string]string{"supervisor_id": fremdID}, http.StatusNotFound)
	admin.expect(http.MethodPatch, "/api/v1/users/"+leadID,
		map[string]string{"manager_id": fremdID}, http.StatusNotFound)
	admin.expect(http.MethodPatch, "/api/v1/org/humans/"+leadID+"/manager",
		map[string]string{"manager_id": fremdID}, http.StatusNotFound)
	admin.expect(http.MethodPost, "/api/v1/departments/"+deptID+"/leads",
		map[string]string{"kind": "human", "member_id": fremdID}, http.StatusNotFound)
}

// TestAgentProfileAndOrgChartQuery deckt die Agenten-Profilfelder ab: Agenten
// tragen dieselben Profilfelder wie Menschen (inkl. der org-weit konfigurierbaren
// Felder), das Löschen einer Feld-Definition räumt auch die Agenten-Werte, und
// ein Agent kann das Organigramm zur Laufzeit über die Meta-Aktion
// covey/org_chart abfragen.
func TestAgentProfileAndOrgChartQuery(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// Org-weit konfigurierbares Profilfeld (key wird aus dem Label abgeleitet).
	field := admin.expect(http.MethodPost, "/api/v1/org/profile-fields",
		map[string]string{"label": "Standort"}, http.StatusCreated)

	agent := s.newSupportAgent("profil-agent")
	agentID := agent.ID.String()

	// Profil schreiben — dieselben Felder wie beim Menschen; Kennungen werden
	// normalisiert (führendes "@" entfällt).
	admin.expect(http.MethodPatch, "/api/v1/agents/"+agentID+"/profile", map[string]any{
		"job_title":        "Support-Spezialist",
		"identities":       map[string]string{"gitlab": "@nova"},
		"responsibilities": "First-Level-Support",
		"custom":           map[string]string{"standort": "Berlin"},
	}, http.StatusOK)

	got := admin.expect(http.MethodGet, "/api/v1/agents/"+agentID, nil, http.StatusOK)
	if got["job_title"] != "Support-Spezialist" || got["responsibilities"] != "First-Level-Support" {
		t.Fatalf("agenten-profil nicht gespeichert: %v", got)
	}
	if ids, _ := got["identities"].(map[string]any); ids["gitlab"] != "nova" {
		t.Fatalf("identities müssen normalisiert sein (ohne @), got %v", got["identities"])
	}
	if custom, _ := got["custom"].(map[string]any); custom["standort"] != "Berlin" {
		t.Fatalf("custom-wert fehlt, got %v", got["custom"])
	}

	// Die Chart-Sicht trägt das Agenten-Profil — dort lesen es Menschen und UI.
	chart := admin.expect(http.MethodGet, "/api/v1/org/chart", nil, http.StatusOK)
	agentsList, _ := chart["agents"].([]any)
	if len(agentsList) != 1 {
		t.Fatalf("erwartet 1 agent im chart, got %d", len(agentsList))
	}
	if a, _ := agentsList[0].(map[string]any); a["job_title"] != "Support-Spezialist" {
		t.Fatalf("chart muss das agenten-profil tragen, got %v", agentsList[0])
	}

	// Der Agent selbst fragt das Organigramm über den Action-Proxy ab
	// (request_org_chart → inject_org_chart) — die Aufgabe läuft nur durch,
	// wenn die Control Plane antwortet.
	task, _ := s.backlog.Create(ctx, s.orgID, agent.ID, "Orgchart-Test",
		`[mock:action covey/org_chart {}]
[mock:result Organigramm gelesen]`, "manual", 3)
	waitFor(t, "aufgabe done", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})
	events, err := s.obs.Events(ctx, agent.ID, nil, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.Kind == "action" && strings.Contains(string(e.Payload), "covey:org_chart") {
			found = true
		}
	}
	if !found {
		t.Fatal("covey:org_chart muss als Aktion im Recording stehen")
	}

	// Feld-Definition löschen räumt den Wert auch aus dem Agenten-Profil.
	admin.expect(http.MethodDelete, "/api/v1/org/profile-fields/"+field["id"].(string), nil, http.StatusOK)
	got = admin.expect(http.MethodGet, "/api/v1/agents/"+agentID, nil, http.StatusOK)
	if custom, _ := got["custom"].(map[string]any); len(custom) != 0 {
		t.Fatalf("gelöschtes profilfeld muss aus agents.custom verschwinden, got %v", got["custom"])
	}
}
