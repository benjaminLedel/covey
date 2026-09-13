package integration

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// The wiki is the agent's long-term memory, and it is correctable by hand —
// that is the whole point of pages instead of flat snippets. These are the
// endpoints a person uses to do the correcting.
func TestWikiPagesOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("gedaechtnis-agent")
	base := "/api/v1/agents/" + agent.ID.String()

	if list := admin.expectList(http.MethodGet, base+"/memories", nil, http.StatusOK); len(list) != 0 {
		t.Errorf("a fresh agent already remembers something: %v", list)
	}

	admin.expect(http.MethodPost, base+"/memories", map[string]any{
		"slug": "kunde-meier", "title": "Kunde Meier", "type": "kunde",
		"tags":    []string{"#Wartung", "wartung"},
		"content": "Meier bezieht seit 2024 die Wartung. Ansprechpartnerin ist Frau Zabel.",
	}, http.StatusCreated)

	list := admin.expectList(http.MethodGet, base+"/memories", nil, http.StatusOK)
	if len(list) != 1 {
		t.Fatalf("the page is not in the memory: %v", list)
	}
	id, _ := list[0]["id"].(string)
	if id == "" {
		t.Fatalf("the page carries no id: %v", list[0])
	}
	// " #Wartung" and "wartung" are one tag — that is what the # stripping is
	// for, and a stray capital or space must not make two of it.
	if tags, _ := list[0]["tags"].([]any); len(tags) != 1 {
		t.Errorf("the page carries %v as tags, expected one", list[0]["tags"])
	}

	// Stock phrases are not knowledge, and a page made of one would be a page
	// the vector search hands back forever.
	admin.expect(http.MethodPost, base+"/memories",
		map[string]any{"slug": "leer", "title": "Leer", "content": "   "}, http.StatusBadRequest)

	admin.expect(http.MethodPatch, "/api/v1/memories/"+id,
		map[string]any{"title": "Kunde Meier GmbH", "content": "Meier bezieht seit 2024 die Wartung."}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/memories/"+id,
		map[string]any{"title": "X", "content": "  "}, http.StatusBadRequest)
	admin.expect(http.MethodPatch, "/api/v1/memories/keine-uuid",
		map[string]any{"title": "X", "content": "etwas"}, http.StatusBadRequest)

	admin.expectList(http.MethodGet, base+"/wiki/log", nil, http.StatusOK)
	health := admin.expect(http.MethodGet, base+"/wiki/health", nil, http.StatusOK)
	if health["pages"] == nil {
		t.Errorf("the health report does not count the pages: %v", health)
	}

	// Consolidation merges near-duplicates. On a wiki with one page it has
	// nothing to do, and that is an answer rather than an error.
	admin.expect(http.MethodPost, base+"/wiki/consolidate", nil, http.StatusOK)

	// Making it forget selectively is the other half of correctable by hand.
	admin.expect(http.MethodDelete, "/api/v1/memories/"+id, nil, http.StatusOK)
	admin.expect(http.MethodDelete, "/api/v1/memories/"+uuid.NewString(), nil, http.StatusNotFound)
	admin.expect(http.MethodDelete, "/api/v1/memories/keine-uuid", nil, http.StatusBadRequest)
	if left := admin.expectList(http.MethodGet, base+"/memories", nil, http.StatusOK); len(left) != 0 {
		t.Errorf("the page survived being forgotten: %v", left)
	}
}

// Every write endpoint reads a body. A body it cannot read is a bad request —
// not a 500, and not a silent default that stores something nobody asked for.
func TestWriteEndpointsRefuseAnUnreadableBody(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("koerper-agent")
	a := "/api/v1/agents/" + agent.ID.String()

	// A JSON array where an object is expected: syntactically fine, and the
	// wrong shape — the case a hand-written client produces.
	const wrongShape = `["nicht", "das", "erwartete"]`

	for _, tc := range []struct{ method, path string }{
		{http.MethodPatch, a + "/name"},
		{http.MethodPatch, a + "/effort"},
		{http.MethodPatch, a + "/model"},
		{http.MethodPatch, a + "/runtime"},
		{http.MethodPatch, a + "/max-turns"},
		{http.MethodPatch, a + "/recording-level"},
		{http.MethodPatch, a + "/recording-retention"},
		{http.MethodPatch, a + "/warm-sandbox"},
		{http.MethodPatch, a + "/runner-tags"},
		{http.MethodPatch, a + "/services"},
		{http.MethodPatch, a + "/sandbox-image"},
		{http.MethodPatch, a + "/slug"},
		{http.MethodPatch, a + "/supervisor"},
		{http.MethodPatch, a + "/department"},
		{http.MethodPost, a + "/budget"},
		{http.MethodPost, a + "/tasks"},
		{http.MethodPost, a + "/memories"},
		{http.MethodPost, a + "/stages"},
		{http.MethodPost, a + "/stages/reorder"},
		{http.MethodPatch, "/api/v1/org/recording-level"},
		{http.MethodPatch, "/api/v1/org/recording-retention"},
		{http.MethodPatch, "/api/v1/org/description"},
		{http.MethodPost, "/api/v1/departments"},
		{http.MethodPost, "/api/v1/org/profile-fields"},
		{http.MethodPost, "/api/v1/runtime-instances"},
		{http.MethodPost, "/api/v1/egress/templates"},
		{http.MethodPost, "/api/v1/egress/defaults"},
		{http.MethodPost, "/api/v1/users"},
		{http.MethodPost, "/api/v1/templates"},
	} {
		resp := admin.doRaw(tc.method, tc.path, wrongShape)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s %s with an unreadable body: HTTP %d, expected 400", tc.method, tc.path, resp.StatusCode)
		}
	}
}
