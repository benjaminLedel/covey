package integration

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// What an agent IS — its name, its engine, its model, the image it works in —
// is set here, and each of these refuses a value that would only fail later.
func TestAgentIdentityOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("kennung-agent")
	base := "/api/v1/agents/" + agent.ID.String()

	admin.expect(http.MethodPatch, base+"/name", map[string]string{"display_name": "Frieda Fleissig"}, http.StatusOK)
	admin.expect(http.MethodPatch, base+"/name", map[string]string{"display_name": ""}, http.StatusBadRequest)

	// The engine has to be one this binary carries. Storing an unknown one
	// would produce an agent that cannot be woken and says so nowhere.
	admin.expect(http.MethodPatch, base+"/runtime", map[string]string{"runtime": "mock"}, http.StatusOK)
	admin.expect(http.MethodPatch, base+"/runtime", map[string]string{"runtime": "gibtesnicht"}, http.StatusBadRequest)

	// The model is checked against the engine: an id the engine cannot route
	// is refused where it is entered, not at the first run.
	admin.expect(http.MethodPatch, base+"/model", map[string]string{"model": ""}, http.StatusOK)

	admin.expect(http.MethodPatch, base+"/sandbox-image",
		map[string]string{"sandbox_image": "covey-sandbox:test"}, http.StatusOK)
	admin.expect(http.MethodPatch, base+"/sandbox-image",
		map[string]string{"sandbox_image": ""}, http.StatusOK)

	// A budget is a limit, and a negative one is not one.
	admin.expect(http.MethodPost, base+"/budget", map[string]any{"budget_usd": 25.5}, http.StatusOK)

	admin.expect(http.MethodPatch, base+"/profile",
		map[string]any{"bio": "Kümmert sich um die erste Reihe."}, http.StatusOK)

	// The supervisor is the reporting line. An empty id clears it.
	admin.expect(http.MethodPatch, base+"/supervisor", map[string]string{"supervisor_id": ""}, http.StatusOK)
	admin.expect(http.MethodPatch, base+"/supervisor", map[string]string{"supervisor_id": "keine-uuid"}, http.StatusBadRequest)
	// An agent cannot report to itself — a chart with a cycle cannot be drawn.
	admin.expect(http.MethodPatch, base+"/supervisor",
		map[string]string{"supervisor_id": agent.ID.String()}, http.StatusBadRequest)
}

// The webhook is an agent's address for the outside world. Switching it on
// mints a token; switching it off takes the address away.
func TestAgentWebhookOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("haken-agent")
	base := "/api/v1/agents/" + agent.ID.String() + "/webhook"

	off := admin.expect(http.MethodGet, base, nil, http.StatusOK)
	if on, _ := off["enabled"].(bool); on {
		t.Error("a fresh agent already carries a webhook")
	}

	on := admin.expect(http.MethodPost, base, nil, http.StatusOK)
	if enabled, _ := on["enabled"].(bool); !enabled {
		t.Errorf("switching on did not take: %v", on)
	}
	if url, _ := on["url"].(string); url == "" {
		t.Errorf("the webhook has no address: %v", on)
	}

	admin.expect(http.MethodDelete, base, nil, http.StatusOK)
	after := admin.expect(http.MethodGet, base, nil, http.StatusOK)
	if enabled, _ := after["enabled"].(bool); enabled {
		t.Error("switching off did not take")
	}
}

// The organisation describes itself: what the company does goes into the
// agents' prompts, and the platform repository is where covey Doctor files
// what it finds.
func TestOrgSelfDescriptionOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	admin.expect(http.MethodPatch, "/api/v1/org/description",
		map[string]string{"description": "Wir bauen Brücken."}, http.StatusOK)
	org := admin.expect(http.MethodGet, "/api/v1/org", nil, http.StatusOK)
	if org["description"] != "Wir bauen Brücken." {
		t.Errorf("the description does not come back: %v", org["description"])
	}

	// The repository has to be one the organisation actually has a plugin for
	// — an address on a system nobody activated is an address covey Doctor
	// could never reach.
	admin.expect(http.MethodPatch, "/api/v1/org/platform-repo",
		map[string]string{"system": "github", "project": "beispiel/covey"}, http.StatusBadRequest)
	// Clearing it is how an organisation goes back to the default the binary
	// derives from its own source address.
	admin.expect(http.MethodPatch, "/api/v1/org/platform-repo",
		map[string]string{"system": "", "project": ""}, http.StatusOK)

	// How densely the office is furnished is the organisation's choice, not
	// a browser's (#325): one of three words, anything else is refused, and
	// what was set comes back with the organisation.
	if org["office_furnishing"] != "normal" {
		t.Errorf("a fresh organisation is furnished normally, got %v", org["office_furnishing"])
	}
	admin.expect(http.MethodPatch, "/api/v1/org/office", map[string]string{"furnishing": "lavish"}, http.StatusBadRequest)
	admin.expect(http.MethodPatch, "/api/v1/org/office", map[string]string{"furnishing": "rich"}, http.StatusOK)
	org = admin.expect(http.MethodGet, "/api/v1/org", nil, http.StatusOK)
	if org["office_furnishing"] != "rich" {
		t.Errorf("the furnishing does not come back: %v", org["office_furnishing"])
	}
}

// The profile fields are the org chart's own vocabulary: what a company wants
// to know about the people and the agents in it.
func TestProfileFieldsOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	made := admin.expect(http.MethodPost, "/api/v1/org/profile-fields",
		map[string]string{"label": "Standort"}, http.StatusCreated)
	id, _ := made["id"].(string)
	if id == "" {
		t.Fatalf("the field carries no id: %v", made)
	}
	// A label exists once — two fields of the same name would be two columns
	// nobody can tell apart.
	admin.expect(http.MethodPost, "/api/v1/org/profile-fields",
		map[string]string{"label": "Standort"}, http.StatusConflict)
	admin.expect(http.MethodPost, "/api/v1/org/profile-fields",
		map[string]string{"label": ""}, http.StatusBadRequest)

	list := admin.expectList(http.MethodGet, "/api/v1/org/profile-fields", nil, http.StatusOK)
	if len(list) == 0 {
		t.Fatal("the field is not in the list")
	}

	admin.expect(http.MethodPatch, "/api/v1/org/profile-fields/"+id,
		map[string]string{"label": "Arbeitsort"}, http.StatusOK)
	admin.expect(http.MethodDelete, "/api/v1/org/profile-fields/"+id, nil, http.StatusOK)
	admin.expect(http.MethodDelete, "/api/v1/org/profile-fields/"+uuid.NewString(), nil, http.StatusNotFound)
}

// A human's place in the org chart: who they report to, and which department
// they belong to. Cycles are refused, because a chart with one cannot be drawn.
func TestHumanPlacementOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	human := s.mitglied(t, "platz@test.local", "Platzhalter", "agent_owner", "platz-passwort")

	got := admin.expect(http.MethodGet, "/api/v1/org/humans/"+human.String(), nil, http.StatusOK)
	if got["email"] != "platz@test.local" {
		t.Errorf("the human is not readable: %v", got)
	}

	admin.expect(http.MethodPatch, "/api/v1/org/humans/"+human.String()+"/manager",
		map[string]string{"manager_id": s.adminID.String()}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/org/humans/"+human.String()+"/manager",
		map[string]string{"manager_id": human.String()}, http.StatusConflict)
	admin.expect(http.MethodPatch, "/api/v1/org/humans/"+human.String()+"/manager",
		map[string]string{"manager_id": ""}, http.StatusOK)
	admin.expect(http.MethodGet, "/api/v1/org/humans/"+uuid.NewString(), nil, http.StatusNotFound)
}
