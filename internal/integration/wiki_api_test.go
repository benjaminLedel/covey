package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	identbuiltin "covey/internal/identity/builtin"
)

// TestWikiTransparencyAPI prüft die Wiki-Transparenz im Webinterface (spec/05):
// benannte Seiten manuell anlegen, das Protokoll lesen, konsolidieren — samt
// RBAC (Lesen für alle, Konsolidieren nur für manage-Rollen).
func TestWikiTransparencyAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	created := admin.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "wiki-api-agent", "display_name": "Wiki-API", "runtime": "mock"}, http.StatusCreated)
	agentID := created["id"].(string)

	// Benannte Seite manuell anlegen (title ⇒ Write, source=manual, Link im Body).
	admin.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/memories",
		map[string]string{"title": "Kunde ACME", "content": "Nur telefonisch erreichbar. Siehe [[projekt-x]]."}, http.StatusCreated)

	// Liste zeigt Titel, Quelle und den extrahierten Wikilink.
	var pages []map[string]any
	resp := admin.do(http.MethodGet, "/api/v1/agents/"+agentID+"/memories", nil)
	json.NewDecoder(resp.Body).Decode(&pages)
	resp.Body.Close()
	if len(pages) != 1 || pages[0]["title"] != "Kunde ACME" || pages[0]["source"] != "manual" {
		t.Fatalf("benannte Seite unerwartet: %+v", pages)
	}
	links, _ := pages[0]["links"].([]any)
	if len(links) != 1 || links[0] != "projekt-x" {
		t.Fatalf("Wikilink muss extrahiert sein, got %+v", pages[0]["links"])
	}

	// Wiki-Protokoll (log.md) zeigt den write-Eintrag zuoberst.
	var logs []map[string]any
	resp = admin.do(http.MethodGet, "/api/v1/agents/"+agentID+"/wiki/log", nil)
	json.NewDecoder(resp.Body).Decode(&logs)
	resp.Body.Close()
	if len(logs) == 0 || logs[0]["op"] != "write" || logs[0]["page_slug"] != "kunde-acme" {
		t.Fatalf("Wiki-Log muss den write-Eintrag zeigen, got %+v", logs)
	}

	// Konsolidieren (manage) liefert die Zahl der Verschmelzungen.
	res := admin.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/wiki/consolidate", nil, http.StatusOK)
	if _, ok := res["merged"]; !ok {
		t.Fatalf("consolidate muss merged liefern, got %+v", res)
	}

	// RBAC: Auditor darf das Protokoll lesen, aber nicht konsolidieren.
	hash, _ := identbuiltin.HashPassword("auditor-passwort")
	if _, err := s.pool.Exec(context.Background(), `INSERT INTO humans (id, org_id, email, display_name, password_hash, role)
		VALUES ($1,$2,'auditor@test.local','Auditor',$3,'auditor')`, uuid.New(), s.orgID, hash); err != nil {
		t.Fatal(err)
	}
	auditor := login(t, s, "auditor@test.local", "auditor-passwort")
	auditor.expect(http.MethodGet, "/api/v1/agents/"+agentID+"/wiki/log", nil, http.StatusOK)
	auditor.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/wiki/consolidate", nil, http.StatusForbidden)
}
