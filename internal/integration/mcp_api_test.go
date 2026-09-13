package integration

import (
	"encoding/json"
	"net/http"
	"testing"
)

// An MCP plugin is installed by naming an endpoint, and that is the one thing
// a reviewer has to look at: installing it opens the organisation's egress to
// that host. So the endpoints around it are where the decision is made.
func TestMCPPluginOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("mcp-agent")

	made := admin.expect(http.MethodPost, "/api/v1/targets/mcp", map[string]any{
		"name": "hauswerkzeug", "label": "Hauswerkzeug",
		"url": "http://127.0.0.1:1/rpc",
	}, http.StatusOK)
	if made == nil {
		t.Fatalf("the plugin was not created: %v", made)
	}

	// A name the rest of the system addresses it by has a shape, and an
	// endpoint without a scheme is not one.
	admin.expect(http.MethodPost, "/api/v1/targets/mcp",
		map[string]any{"name": "Falsch Geschrieben", "url": "http://127.0.0.1:1"}, http.StatusBadRequest)
	admin.expect(http.MethodPost, "/api/v1/targets/mcp",
		map[string]any{"name": "ohne-schema", "url": "mcp.example.test"}, http.StatusBadRequest)

	list := admin.expectList(http.MethodGet, "/api/v1/targets", nil, http.StatusOK)
	var found bool
	for _, tgt := range list {
		if tgt["name"] == "hauswerkzeug" {
			found = true
		}
	}
	if !found {
		t.Errorf("the MCP plugin is not among the target systems: %v", list)
	}

	// The setup document is what an operator reads before connecting it.
	admin.expect(http.MethodGet, "/api/v1/targets/hauswerkzeug/setup", nil, http.StatusOK)

	// The cached tool list: empty until a discovery has run, and an empty list
	// is the honest answer rather than a 404.
	admin.expectList(http.MethodGet, "/api/v1/targets/hauswerkzeug/tools", nil, http.StatusOK)

	// Which of a system's tools an agent may use is narrowed per agent, and an
	// empty list is how everything is taken away.
	base := "/api/v1/agents/" + agent.ID.String() + "/tools/hauswerkzeug"
	admin.expectList(http.MethodGet, base, nil, http.StatusOK)
	admin.expect(http.MethodPut, base, map[string]any{"tools": []string{"lesen"}}, http.StatusOK)
	got := admin.expectList(http.MethodGet, base, nil, http.StatusOK)
	if len(got) != 1 {
		t.Errorf("the agent's tool list is %v", got)
	}
	admin.expect(http.MethodPut, base, map[string]any{"tools": []string{}}, http.StatusOK)
	// A body without the field at all takes everything away too — the list is
	// the statement, and its absence is an empty one.
	admin.expect(http.MethodPut, base, map[string]any{}, http.StatusOK)
	admin.expect(http.MethodPut, base, "kein json", http.StatusBadRequest)

	admin.expect(http.MethodDelete, "/api/v1/targets/hauswerkzeug", nil, http.StatusOK)
	admin.expect(http.MethodDelete, "/api/v1/targets/hauswerkzeug", nil, http.StatusNotFound)
}

// A manifest plugin is a JSON file an organisation uploads, and from then on
// it is a target system like any other — that is the whole point of the plugin
// machinery: no privileged tier for the ones we wrote.
func TestManifestPluginOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("manifest-agent")

	const manifest = `{
	  "name": "hausticket",
	  "label": "Hausticket",
	  "category": "ticketing",
	  "auth": {"header": "X-API-Key", "format": "{token}"},
	  "scopes": ["read", "write"],
	  "webhook": {"signature": "hmac-sha256", "id_field": "issue.id", "title_field": "issue.title"},
	  "actions": {
	    "get_issue": {"method": "GET", "path": "/issues/{issue_id}", "scope": "read", "doc": "reads one issue"},
	    "comment": {"method": "POST", "path": "/issues/{issue_id}/comments", "scope": "write", "doc": "answers"}
	  },
	  "prompt_doc": "Available hausticket actions: get_issue, comment."
	}`

	made := admin.expect(http.MethodPost, "/api/v1/targets", json.RawMessage(manifest), http.StatusOK)
	if made["name"] != "hausticket" || made["kind"] != "custom" {
		t.Fatalf("the upload answered %v", made)
	}

	// A manifest that is not one is refused with the reason — an author gets
	// the same sentence here as from `covey plugin lint`.
	admin.expect(http.MethodPost, "/api/v1/targets", json.RawMessage(`{"name":"Falsch"}`), http.StatusBadRequest)

	list := admin.expectList(http.MethodGet, "/api/v1/targets", nil, http.StatusOK)
	var found bool
	for _, tgt := range list {
		if tgt["name"] == "hausticket" {
			found = true
		}
	}
	if !found {
		t.Errorf("the uploaded plugin is not among the target systems: %v", list)
	}

	// The setup document is assembled from the manifest — what an operator has
	// to enter to connect it.
	admin.expect(http.MethodGet, "/api/v1/targets/hausticket/setup", nil, http.StatusOK)

	// Switching it off takes it out of circulation without deleting it: the
	// configuration stays, and the agents that name it stop reaching it.
	admin.expect(http.MethodPatch, "/api/v1/targets/hausticket",
		map[string]any{"enabled": false}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/targets/hausticket",
		map[string]any{}, http.StatusBadRequest)
	admin.expect(http.MethodPatch, "/api/v1/targets/hausticket",
		map[string]any{"enabled": true}, http.StatusOK)

	// Which of its actions an agent may use is narrowed per agent.
	base := "/api/v1/agents/" + agent.ID.String() + "/tools/hausticket"
	admin.expect(http.MethodPut, base, map[string]any{"tools": []string{"get_issue"}}, http.StatusOK)
	resp := admin.do(http.MethodGet, base, nil)
	var tools []string
	json.NewDecoder(resp.Body).Decode(&tools)
	resp.Body.Close()
	if len(tools) != 1 || tools[0] != "get_issue" {
		t.Errorf("the agent's tool list is %v", tools)
	}

	admin.expect(http.MethodDelete, "/api/v1/targets/hausticket", nil, http.StatusOK)
}
