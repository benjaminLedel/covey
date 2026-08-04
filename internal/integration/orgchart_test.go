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

// TestOrgChart covers the org chart: supervisor relations for humans
// (manager_id, cycle-free) and agents (supervisor_id), the chart view readable
// for every role, and the org scoping of the supervisor.
func TestOrgChart(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// Create a lead that reports to the admin.
	lead := admin.expect(http.MethodPost, "/api/v1/users", map[string]string{
		"email": "lead@test.local", "display_name": "Lead", "role": "agent_owner",
		"password": "lead-passwort",
	}, http.StatusCreated)
	leadID := lead["id"].(string)
	admin.expect(http.MethodPatch, "/api/v1/users/"+leadID,
		map[string]string{"manager_id": s.adminID.String()}, http.StatusOK)

	// Cycles are forbidden: neither self-reference nor admin → lead → admin.
	admin.expect(http.MethodPatch, "/api/v1/users/"+leadID,
		map[string]string{"manager_id": leadID}, http.StatusConflict)
	admin.expect(http.MethodPatch, "/api/v1/users/"+s.adminID.String(),
		map[string]string{"manager_id": leadID}, http.StatusConflict)

	// The org-chart endpoint (drag & drop in the UI) can re-hang humans just as
	// well: cycles are rejected, an empty manager_id clears the assignment.
	admin.expect(http.MethodPatch, "/api/v1/org/humans/"+leadID+"/manager",
		map[string]string{"manager_id": leadID}, http.StatusConflict)
	admin.expect(http.MethodPatch, "/api/v1/org/humans/"+leadID+"/manager",
		map[string]string{"manager_id": ""}, http.StatusOK)
	if h := admin.expect(http.MethodGet, "/api/v1/org/humans/"+leadID, nil, http.StatusOK); h["manager_id"] != nil {
		t.Fatalf("manager_id has to be cleared, got %v", h["manager_id"])
	}
	admin.expect(http.MethodPatch, "/api/v1/org/humans/"+leadID+"/manager",
		map[string]string{"manager_id": s.adminID.String()}, http.StatusOK)

	// Create an agent and assign it to the lead.
	created := admin.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "nova", "display_name": "Nova"}, http.StatusCreated)
	agentID := created["id"].(string)
	admin.expect(http.MethodPatch, "/api/v1/agents/"+agentID+"/supervisor",
		map[string]string{"supervisor_id": leadID}, http.StatusOK)

	// The chart is readable for every role and contains both relations.
	viewer := login(t, s, "lead@test.local", "lead-passwort")
	resp := viewer.do(http.MethodGet, "/api/v1/org/chart", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("org/chart expected 200, got %d", resp.StatusCode)
	}
	var chart struct {
		Humans []map[string]any `json:"humans"`
		Agents []map[string]any `json:"agents"`
	}
	json.NewDecoder(resp.Body).Decode(&chart)
	resp.Body.Close()
	if len(chart.Humans) != 2 || len(chart.Agents) != 1 {
		t.Fatalf("expected 2 humans / 1 agent, got %d/%d", len(chart.Humans), len(chart.Agents))
	}
	for _, h := range chart.Humans {
		if h["email"] == "lead@test.local" && h["manager_id"] != s.adminID.String() {
			t.Fatalf("the lead has to report to the admin, got %v", h["manager_id"])
		}
	}
	if chart.Agents[0]["supervisor_id"] != leadID {
		t.Fatalf("the agent has to report to the lead, got %v", chart.Agents[0]["supervisor_id"])
	}

	// An empty supervisor_id clears the assignment.
	admin.expect(http.MethodPatch, "/api/v1/agents/"+agentID+"/supervisor",
		map[string]string{"supervisor_id": ""}, http.StatusOK)
	got := admin.expect(http.MethodGet, "/api/v1/agents/"+agentID, nil, http.StatusOK)
	if _, hasSupervisor := got["supervisor_id"]; hasSupervisor {
		t.Fatalf("supervisor_id has to be cleared, got %v", got["supervisor_id"])
	}

	// Departments can have one or several leads — humans as well as agents.
	// Assigning is idempotent, the leads are part of the department response,
	// removing deletes exactly the one assignment.
	dept := admin.expect(http.MethodPost, "/api/v1/departments",
		map[string]string{"name": "Support", "color": "#7d9471"}, http.StatusCreated)
	deptID := dept["id"].(string)
	if dept["color"] != "#7d9471" {
		t.Fatalf("the department has to carry the color, got %v", dept["color"])
	}

	// Change and reset the color; broken values are rejected.
	admin.expect(http.MethodPatch, "/api/v1/departments/"+deptID+"/color",
		map[string]string{"color": "#c9a227"}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/departments/"+deptID+"/color",
		map[string]string{"color": "red"}, http.StatusBadRequest)
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
		t.Fatalf("expected 1 department with 2 leads, got %+v", depts)
	}

	admin.expect(http.MethodDelete, "/api/v1/departments/"+deptID+"/leads/"+agentID, nil, http.StatusOK)
	admin.expect(http.MethodDelete, "/api/v1/departments/"+deptID+"/leads/"+agentID, nil, http.StatusNotFound)

	// Org scoping: a human from a foreign organization is not a valid supervisor
	// — neither for agents nor for humans.
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

// TestAgentProfileAndOrgChartQuery covers the agent profile fields: agents
// carry the same profile fields as humans (including the org-wide configurable
// ones), deleting a field definition also clears the agent values, and an agent
// can query the org chart at runtime through the meta action covey/org_chart.
func TestAgentProfileAndOrgChartQuery(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// Org-wide configurable profile field (the key is derived from the label).
	field := admin.expect(http.MethodPost, "/api/v1/org/profile-fields",
		map[string]string{"label": "Standort"}, http.StatusCreated)

	agent := s.newSupportAgent("profil-agent")
	agentID := agent.ID.String()

	// Write the profile — the same fields as for a human; identifiers are
	// normalized (a leading "@" is dropped).
	admin.expect(http.MethodPatch, "/api/v1/agents/"+agentID+"/profile", map[string]any{
		"job_title":        "Support-Spezialist",
		"identities":       map[string]string{"gitlab": "@nova"},
		"responsibilities": "First-Level-Support",
		"custom":           map[string]string{"standort": "Berlin"},
	}, http.StatusOK)

	got := admin.expect(http.MethodGet, "/api/v1/agents/"+agentID, nil, http.StatusOK)
	if got["job_title"] != "Support-Spezialist" || got["responsibilities"] != "First-Level-Support" {
		t.Fatalf("the agent profile was not stored: %v", got)
	}
	if ids, _ := got["identities"].(map[string]any); ids["gitlab"] != "nova" {
		t.Fatalf("identities have to be normalized (without @), got %v", got["identities"])
	}
	if custom, _ := got["custom"].(map[string]any); custom["standort"] != "Berlin" {
		t.Fatalf("the custom value is missing, got %v", got["custom"])
	}

	// The chart view carries the agent profile — that is where humans and UI read it.
	chart := admin.expect(http.MethodGet, "/api/v1/org/chart", nil, http.StatusOK)
	agentsList, _ := chart["agents"].([]any)
	if len(agentsList) != 1 {
		t.Fatalf("expected 1 agent in the chart, got %d", len(agentsList))
	}
	if a, _ := agentsList[0].(map[string]any); a["job_title"] != "Support-Spezialist" {
		t.Fatalf("the chart has to carry the agent profile, got %v", agentsList[0])
	}

	// The agent itself queries the org chart through the action proxy
	// (request_org_chart → inject_org_chart) — the task only runs through if the
	// control plane answers.
	task, _ := s.backlog.Create(ctx, s.orgID, agent.ID, "Orgchart-Test",
		`[mock:action covey/org_chart {}]
[mock:result org chart read]`, "manual", 3)
	waitFor(t, "task done", 15*time.Second, func() bool {
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
		t.Fatal("covey:org_chart has to appear as an action in the recording")
	}

	// Deleting the field definition also clears the value from the agent profile.
	admin.expect(http.MethodDelete, "/api/v1/org/profile-fields/"+field["id"].(string), nil, http.StatusOK)
	got = admin.expect(http.MethodGet, "/api/v1/agents/"+agentID, nil, http.StatusOK)
	if custom, _ := got["custom"].(map[string]any); len(custom) != 0 {
		t.Fatalf("a deleted profile field has to disappear from agents.custom, got %v", got["custom"])
	}
}
