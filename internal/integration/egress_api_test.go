package integration

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// The egress allowlist is the platform's own boundary around a sandbox
// (spec/06, principle #7): an agent reaches what stands here and nothing else.
// It is administered exclusively over these endpoints, so what they refuse is
// as load-bearing as what they store.
func TestEgressTemplatesOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// The status carries three things apart: what enforcement is on, the
	// org's own base allowlist, and what only the environment can change.
	st := admin.expect(http.MethodGet, "/api/v1/egress", nil, http.StatusOK)
	for _, key := range []string{"enforced", "defaults", "env"} {
		if _, ok := st[key]; !ok {
			t.Errorf("the status does not carry %q: %v", key, st)
		}
	}

	tpl := admin.expect(http.MethodPost, "/api/v1/egress/templates",
		map[string]string{"name": "Zammad", "description": "the ticket system"}, http.StatusOK)
	tid, _ := tpl["id"].(string)
	if tid == "" {
		t.Fatalf("the template carries no id: %v", tpl)
	}

	// A name exists once per organisation — otherwise two templates of the
	// same name would be two allowlists nobody can tell apart in the interface.
	admin.expect(http.MethodPost, "/api/v1/egress/templates",
		map[string]string{"name": "Zammad"}, http.StatusConflict)

	list := admin.expectList(http.MethodGet, "/api/v1/egress/templates", nil, http.StatusOK)
	if len(list) != 1 || list[0]["name"] != "Zammad" {
		t.Fatalf("the template is not in the list: %v", list)
	}

	host := admin.expect(http.MethodPost, "/api/v1/egress/templates/"+tid+"/hosts",
		map[string]string{"pattern": "zammad.example.test", "note": "API"}, http.StatusOK)
	hid, _ := host["id"].(string)
	if hid == "" {
		t.Fatalf("the host carries no id: %v", host)
	}

	// A pattern that is not one is refused where it is entered. The alternative
	// is a rule that lets nothing through and says so only in the proxy log.
	admin.expect(http.MethodPost, "/api/v1/egress/templates/"+tid+"/hosts",
		map[string]string{"pattern": "https://zammad.example.test/api"}, http.StatusBadRequest)

	admin.expect(http.MethodDelete, "/api/v1/egress/template-hosts/"+hid, nil, http.StatusOK)
	admin.expect(http.MethodDelete, "/api/v1/egress/templates/"+tid, nil, http.StatusOK)
	if left := admin.expectList(http.MethodGet, "/api/v1/egress/templates", nil, http.StatusOK); len(left) != 0 {
		t.Errorf("the template survived its deletion: %v", left)
	}

	// An id that is no id is a bad request, not a 500 and not a silent 404.
	admin.expect(http.MethodDelete, "/api/v1/egress/templates/keine-uuid", nil, http.StatusBadRequest)
	admin.expect(http.MethodDelete, "/api/v1/egress/template-hosts/keine-uuid", nil, http.StatusBadRequest)
	admin.expect(http.MethodDelete, "/api/v1/egress/templates/"+uuid.NewString(), nil, http.StatusNotFound)
}

// The catalogue exists so that nobody has to assemble a target system's hosts
// by hand. What matters is that an imported entry becomes an ordinary,
// editable template of the organisation — and says so in the listing.
func TestEgressBuiltinCatalogueOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	builtins := admin.expectList(http.MethodGet, "/api/v1/egress/builtin", nil, http.StatusOK)
	if len(builtins) == 0 {
		t.Fatal("the catalogue is empty — the setup would have nothing to offer")
	}
	var slug string
	for _, b := range builtins {
		if imported, _ := b["imported"].(bool); imported {
			t.Errorf("%v counts as imported in a fresh organisation", b["slug"])
		}
		if slug == "" {
			slug, _ = b["slug"].(string)
		}
	}
	if slug == "" {
		t.Fatal("no catalogue entry carries a slug")
	}

	imported := admin.expect(http.MethodPost, "/api/v1/egress/builtin/"+slug, nil, http.StatusOK)
	tid, _ := imported["id"].(string)
	if tid == "" {
		t.Fatalf("the import returned no template: %v", imported)
	}

	// Twice is a conflict, not a second copy.
	admin.expect(http.MethodPost, "/api/v1/egress/builtin/"+slug, nil, http.StatusConflict)
	admin.expect(http.MethodPost, "/api/v1/egress/builtin/gibtesnicht", nil, http.StatusNotFound)

	after := admin.expectList(http.MethodGet, "/api/v1/egress/builtin", nil, http.StatusOK)
	found := false
	for _, b := range after {
		if b["slug"] == slug {
			found = true
			if imported, _ := b["imported"].(bool); !imported {
				t.Error("the imported entry is not marked as imported")
			}
			if b["template_id"] != tid {
				t.Errorf("the entry points at %v, expected %s", b["template_id"], tid)
			}
		}
	}
	if !found {
		t.Errorf("%s disappeared from the catalogue after the import", slug)
	}
}

// The base allowlist applies to every agent of the organisation, which is why
// it is administered apart from the templates.
func TestEgressDefaultHostsOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	h := admin.expect(http.MethodPost, "/api/v1/egress/defaults",
		map[string]string{"pattern": "*.example.test", "note": "everything of ours"}, http.StatusOK)
	id, _ := h["id"].(string)
	if id == "" {
		t.Fatalf("the default host carries no id: %v", h)
	}
	st := admin.expect(http.MethodGet, "/api/v1/egress", nil, http.StatusOK)
	defaults, _ := st["defaults"].([]any)
	if len(defaults) != 1 {
		t.Fatalf("the base allowlist holds %d entries, expected 1", len(defaults))
	}

	admin.expect(http.MethodPost, "/api/v1/egress/defaults",
		map[string]string{"pattern": "http://example.test/"}, http.StatusBadRequest)
	admin.expect(http.MethodDelete, "/api/v1/egress/defaults/"+id, nil, http.StatusOK)
	admin.expect(http.MethodDelete, "/api/v1/egress/defaults/keine-uuid", nil, http.StatusBadRequest)
}

// What an agent may reach is three things added together: the base allowlist,
// the templates assigned to it, and the hosts it carries itself.
func TestAgentEgressOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("egress-agent")
	base := "/api/v1/agents/" + agent.ID.String() + "/egress"

	tpl := admin.expect(http.MethodPost, "/api/v1/egress/templates",
		map[string]string{"name": "GitLab"}, http.StatusOK)
	tid := tpl["id"].(string)
	admin.expect(http.MethodPost, "/api/v1/egress/templates/"+tid+"/hosts",
		map[string]string{"pattern": "gitlab.example.test"}, http.StatusOK)

	cfg := admin.expect(http.MethodGet, base, nil, http.StatusOK)
	if cfg == nil {
		t.Fatal("the agent carries no egress configuration")
	}

	admin.expect(http.MethodPut, base+"/templates/"+tid, nil, http.StatusOK)
	admin.expect(http.MethodPut, base+"/templates/keine-uuid", nil, http.StatusBadRequest)

	own := admin.expect(http.MethodPost, base+"/hosts",
		map[string]string{"pattern": "nur-fuer-diesen.example.test"}, http.StatusOK)
	hid, _ := own["id"].(string)
	if hid == "" {
		t.Fatalf("the agent's own host carries no id: %v", own)
	}
	admin.expect(http.MethodPost, base+"/hosts",
		map[string]string{"pattern": "kein muster"}, http.StatusBadRequest)

	// Taking the template away leaves the agent's own host standing — the two
	// are separate statements, and a withdrawal must not quietly take both.
	admin.expect(http.MethodDelete, base+"/templates/"+tid, nil, http.StatusOK)
	admin.expect(http.MethodDelete, base+"/hosts/"+hid, nil, http.StatusOK)
	admin.expect(http.MethodDelete, base+"/hosts/keine-uuid", nil, http.StatusBadRequest)
}

// The log is what turns a refusal into something readable. Its filters are the
// only way anybody gets at it, so they are worth a test of their own.
func TestEgressLogAndStatsOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("egress-log-agent")

	admin.expectList(http.MethodGet, "/api/v1/egress/log", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/egress/log?blocked=true&limit=5", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/egress/log?agent="+agent.ID.String(), nil, http.StatusOK)
	admin.expect(http.MethodGet, "/api/v1/egress/stats", nil, http.StatusOK)

	// An unreadable agent id is a bad request; a foreign one is not found —
	// and not "empty", which would say the agent exists and did nothing.
	admin.expect(http.MethodGet, "/api/v1/egress/log?agent=keine-uuid", nil, http.StatusBadRequest)
	admin.expect(http.MethodGet, "/api/v1/egress/log?agent="+uuid.NewString(), nil, http.StatusNotFound)
}
