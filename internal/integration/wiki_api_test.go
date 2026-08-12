package integration

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestWikiTransparencyAPI checks the wiki transparency in the web interface
// (spec/05): creating named pages by hand, reading the log, consolidating —
// including RBAC (reading for everyone, consolidating only for manage roles).
func TestWikiTransparencyAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	created := admin.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "wiki-api-agent", "display_name": "Wiki-API", "runtime": "mock"}, http.StatusCreated)
	agentID := created["id"].(string)

	// Create a named page by hand (title ⇒ Write, source=manual, link in the body).
	admin.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/memories",
		map[string]string{"title": "Kunde ACME", "content": "Nur telefonisch erreichbar. Siehe [[projekt-x]]."}, http.StatusCreated)

	// The list shows title, source and the extracted wikilink.
	var pages []map[string]any
	resp := admin.do(http.MethodGet, "/api/v1/agents/"+agentID+"/memories", nil)
	json.NewDecoder(resp.Body).Decode(&pages)
	resp.Body.Close()
	if len(pages) != 1 || pages[0]["title"] != "Kunde ACME" || pages[0]["source"] != "manual" {
		t.Fatalf("the named page is unexpected: %+v", pages)
	}
	links, _ := pages[0]["links"].([]any)
	if len(links) != 1 || links[0] != "projekt-x" {
		t.Fatalf("the wikilink has to be extracted, got %+v", pages[0]["links"])
	}

	// The wiki log (log.md) shows the write entry at the top.
	var logs []map[string]any
	resp = admin.do(http.MethodGet, "/api/v1/agents/"+agentID+"/wiki/log", nil)
	json.NewDecoder(resp.Body).Decode(&logs)
	resp.Body.Close()
	if len(logs) == 0 || logs[0]["op"] != "write" || logs[0]["page_slug"] != "kunde-acme" {
		t.Fatalf("the wiki log has to show the write entry, got %+v", logs)
	}

	// Consolidating (manage) returns the number of merges.
	res := admin.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/wiki/consolidate", nil, http.StatusOK)
	if _, ok := res["merged"]; !ok {
		t.Fatalf("consolidate has to return merged, got %+v", res)
	}

	// RBAC: the auditor may read the log but not consolidate.
	s.mitglied(t, "auditor@test.local", "Auditor", "auditor", "auditor-passwort")
	auditor := login(t, s, "auditor@test.local", "auditor-passwort")
	auditor.expect(http.MethodGet, "/api/v1/agents/"+agentID+"/wiki/log", nil, http.StatusOK)
	auditor.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/wiki/consolidate", nil, http.StatusForbidden)
}
