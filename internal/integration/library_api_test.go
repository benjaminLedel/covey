package integration

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// The template library is where an agent that works becomes a starting point
// for the next one. Two kinds live in one list: the bundled ones, shared and
// read-only, and the organisation's own.
func TestTemplateLibraryOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("vorlagen-agent")

	bundled := admin.expectList(http.MethodGet, "/api/v1/templates?lang=de", nil, http.StatusOK)
	if len(bundled) == 0 {
		t.Fatal("the library is empty — a fresh installation would have nothing to start from")
	}
	var builtinID string
	for _, tpl := range bundled {
		if b, _ := tpl["builtin"].(bool); b {
			builtinID, _ = tpl["id"].(string)
			break
		}
	}
	if builtinID == "" {
		t.Fatal("no bundled template in the list")
	}

	// The language decides name and description; the ids stay the same, so a
	// link into the library survives a language change.
	en := admin.expectList(http.MethodGet, "/api/v1/templates?lang=en", nil, http.StatusOK)
	if len(en) != len(bundled) {
		t.Errorf("the two languages carry different numbers of templates (%d/%d)", len(bundled), len(en))
	}

	// A bundled template is read-only. Deleting it has to say so — the
	// alternative is a delete that reports success and changes nothing.
	admin.expect(http.MethodDelete, "/api/v1/templates/"+builtinID, nil, http.StatusForbidden)

	got := admin.expect(http.MethodGet, "/api/v1/templates/"+builtinID, nil, http.StatusOK)
	if got["id"] != builtinID {
		t.Errorf("the library returned %v, expected %s", got["id"], builtinID)
	}
	admin.expect(http.MethodGet, "/api/v1/templates/keine-uuid", nil, http.StatusBadRequest)
	admin.expect(http.MethodGet, "/api/v1/templates/"+uuid.NewString(), nil, http.StatusNotFound)

	// An agent's own configuration becomes a template of the organisation.
	own := admin.expect(http.MethodPost, "/api/v1/templates", map[string]string{
		"name": "Support wie gehabt", "description": "aus einem laufenden Agenten",
		"from_agent_id": agent.ID.String(),
	}, http.StatusCreated)
	ownID, _ := own["id"].(string)
	if ownID == "" {
		t.Fatalf("the saved template carries no id: %v", own)
	}
	if b, _ := own["builtin"].(bool); b {
		t.Error("an organisation's own template is marked as bundled")
	}

	admin.expect(http.MethodPost, "/api/v1/templates",
		map[string]string{"from_agent_id": agent.ID.String()}, http.StatusBadRequest)
	admin.expect(http.MethodPost, "/api/v1/templates",
		map[string]string{"name": "ohne Agenten"}, http.StatusBadRequest)
	admin.expect(http.MethodPost, "/api/v1/templates",
		map[string]string{"name": "X", "from_agent_id": uuid.NewString()}, http.StatusNotFound)

	// And from the template comes the next agent.
	made := admin.expect(http.MethodPost, "/api/v1/templates/"+ownID+"/instantiate",
		map[string]string{"slug": "support-zwei", "display_name": "Support Zwei"}, http.StatusCreated)
	newAgent, _ := made["agent"].(map[string]any)
	if newAgent == nil || newAgent["slug"] != "support-zwei" {
		t.Errorf("the new agent carries the slug %v", made["agent"])
	}
	// The webhook is deliberately NOT switched on: the token would be a new
	// one, so nothing would be reachable under the old address anyway.
	if on, _ := newAgent["webhook_enabled"].(bool); on {
		t.Error("instantiating switched the webhook on")
	}
	admin.expect(http.MethodPost, "/api/v1/templates/"+ownID+"/instantiate",
		map[string]string{"display_name": "ohne Slug"}, http.StatusBadRequest)
	admin.expect(http.MethodPost, "/api/v1/templates/"+uuid.NewString()+"/instantiate",
		map[string]string{"slug": "x"}, http.StatusNotFound)
	admin.expect(http.MethodPost, "/api/v1/templates/keine-uuid/instantiate",
		map[string]string{"slug": "x"}, http.StatusBadRequest)

	admin.expect(http.MethodDelete, "/api/v1/templates/"+ownID, nil, http.StatusOK)
	admin.expect(http.MethodDelete, "/api/v1/templates/"+ownID, nil, http.StatusNotFound)
	admin.expect(http.MethodDelete, "/api/v1/templates/keine-uuid", nil, http.StatusBadRequest)
}

// Departments are the org chart's second axis: who belongs together, apart from
// who reports to whom. Humans and agents hang in the same structure.
func TestDepartmentsOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("abteilungs-agent")

	dep := admin.expect(http.MethodPost, "/api/v1/departments", map[string]string{
		"name": "Support", "description": "erste Reihe", "color": "#cc7a5b",
	}, http.StatusCreated)
	id, _ := dep["id"].(string)
	if id == "" {
		t.Fatalf("the department carries no id: %v", dep)
	}

	// A colour that is not one would be a swatch the interface cannot paint.
	admin.expect(http.MethodPost, "/api/v1/departments",
		map[string]string{"name": "Falschfarbe", "color": "rot"}, http.StatusBadRequest)
	admin.expect(http.MethodPost, "/api/v1/departments",
		map[string]string{"name": ""}, http.StatusBadRequest)

	list := admin.expectList(http.MethodGet, "/api/v1/departments", nil, http.StatusOK)
	if len(list) == 0 {
		t.Fatal("the department is not in the list")
	}

	admin.expect(http.MethodPatch, "/api/v1/departments/"+id+"/name",
		map[string]string{"name": "Kundendienst"}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/departments/"+id+"/color",
		map[string]string{"color": "#123456"}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/departments/"+id+"/color",
		map[string]string{"color": "#xyz"}, http.StatusBadRequest)
	admin.expect(http.MethodPatch, "/api/v1/departments/"+uuid.NewString()+"/name",
		map[string]string{"name": "X"}, http.StatusNotFound)
	admin.expect(http.MethodPatch, "/api/v1/departments/keine-uuid/name",
		map[string]string{"name": "X"}, http.StatusBadRequest)

	// A lead is a human or an agent — the department does not care which, and
	// that is the point of the org chart carrying both.
	human := s.mitglied(t, "lead-dep@test.local", "Lead", "agent_owner", "lead-passwort")
	admin.expect(http.MethodPost, "/api/v1/departments/"+id+"/leads",
		map[string]string{"kind": "human", "member_id": human.String()}, http.StatusOK)
	admin.expect(http.MethodPost, "/api/v1/departments/"+id+"/leads",
		map[string]string{"kind": "agent", "member_id": agent.ID.String()}, http.StatusOK)
	admin.expect(http.MethodPost, "/api/v1/departments/"+id+"/leads",
		map[string]string{"kind": "roboter", "member_id": agent.ID.String()}, http.StatusBadRequest)
	admin.expect(http.MethodPost, "/api/v1/departments/"+id+"/leads",
		map[string]string{"kind": "agent", "member_id": "keine-uuid"}, http.StatusBadRequest)
	admin.expect(http.MethodDelete, "/api/v1/departments/"+id+"/leads/"+human.String(), nil, http.StatusOK)
	admin.expect(http.MethodDelete, "/api/v1/departments/"+id+"/leads/keine-uuid", nil, http.StatusBadRequest)

	// Agents and humans are put into a department the same way, and the empty
	// id is how they are taken out again.
	admin.expect(http.MethodPatch, "/api/v1/agents/"+agent.ID.String()+"/department",
		map[string]string{"department_id": id}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/agents/"+agent.ID.String()+"/department",
		map[string]string{"department_id": ""}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/agents/"+agent.ID.String()+"/department",
		map[string]string{"department_id": "keine-uuid"}, http.StatusBadRequest)
	admin.expect(http.MethodPatch, "/api/v1/agents/"+agent.ID.String()+"/department",
		map[string]string{"department_id": uuid.NewString()}, http.StatusNotFound)

	admin.expect(http.MethodPatch, "/api/v1/org/humans/"+human.String()+"/department",
		map[string]string{"department_id": id}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/org/humans/"+human.String()+"/department",
		map[string]string{"department_id": ""}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/org/humans/keine-uuid/department",
		map[string]string{"department_id": id}, http.StatusBadRequest)

	admin.expect(http.MethodDelete, "/api/v1/departments/"+id, nil, http.StatusOK)
	admin.expect(http.MethodDelete, "/api/v1/departments/"+uuid.NewString(), nil, http.StatusNotFound)
}

// The dream writes unasked, at night, with nobody watching — so what it did is
// readable afterwards and every action can be taken back individually.
func TestDreamsOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("traum-agent")
	base := "/api/v1/agents/" + agent.ID.String() + "/dreams"

	if list := admin.expectList(http.MethodGet, base, nil, http.StatusOK); len(list) != 0 {
		t.Errorf("a fresh agent has already dreamt: %v", list)
	}

	// Without a model credential there is no dream, and the refusal says which
	// of the two preconditions is missing rather than failing at the first
	// attempt to think.
	admin.expect(http.MethodPost, base, nil, http.StatusPreconditionFailed)

	admin.expect(http.MethodGet, "/api/v1/agents/keine-uuid/dreams", nil, http.StatusBadRequest)
	admin.expect(http.MethodPost, "/api/v1/dream-actions/"+uuid.NewString()+"/undo", nil, http.StatusNotFound)
	admin.expect(http.MethodPost, "/api/v1/dream-actions/keine-uuid/undo", nil, http.StatusBadRequest)
}
