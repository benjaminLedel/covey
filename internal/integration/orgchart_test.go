package integration

import (
	"encoding/json"
	"net/http"
	"testing"
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
}
