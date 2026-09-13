package integration

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Every read endpoint has to answer on a FRESH installation — the state in
// which a new instance is looked at for the first time. A handler that assumes
// a row is there fails exactly then, in front of the person deciding whether to
// keep the thing.
//
// The endpoints are listed rather than discovered, because the list IS the
// claim: what is missing here is an endpoint nobody drives.
func TestEveryReadEndpointAnswersOnAFreshInstallation(t *testing.T) {
	s := newStack(t)
	s.alsSystemadmin(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("oberflaechen-agent")
	a := agent.ID.String()

	orgWide := []string{
		"/api/v1/version",
		"/api/v1/auth/me",
		"/api/v1/auth/me/profile",
		"/api/v1/auth/sessions",
		"/api/v1/auth/notifications",
		"/api/v1/auth/api-keys",
		"/api/v1/setup/state",
		"/api/v1/audit",
		"/api/v1/onboarding",
		"/api/v1/agents",
		"/api/v1/assist/status",
		"/api/v1/names/roll",
		"/api/v1/runners",
		"/api/v1/runners/health",
		"/api/v1/runners/registration-tokens",
		"/api/v1/workplaces",
		"/api/v1/service-images",
		"/api/v1/platform/doctor",
		"/api/v1/platform/lint",
		"/api/v1/platform/home-store",
		"/api/v1/org/recording-level",
		"/api/v1/departments",
		"/api/v1/org",
		"/api/v1/org/chart",
		"/api/v1/org/profile-fields",
		"/api/v1/runtimes",
		"/api/v1/runtime-instances",
		"/api/v1/marketplace",
		"/api/v1/targets",
		"/api/v1/cost/org",
		"/api/v1/cost/runs",
		"/api/v1/cost/indicators",
		"/api/v1/approvals",
		"/api/v1/inbox",
		"/api/v1/improvements",
		"/api/v1/guardrails",
		"/api/v1/guardrails/events",
		"/api/v1/egress",
		"/api/v1/egress/templates",
		"/api/v1/egress/log",
		"/api/v1/egress/stats",
		"/api/v1/egress/builtin",
		"/api/v1/secrets",
		"/api/v1/fleet",
		"/api/v1/skills",
		"/api/v1/templates",
		"/api/v1/users",
		"/api/v1/platform/orgs",
		"/api/v1/platform/accounts",
		"/api/v1/platform/settings",
		"/api/v1/platform/waitlist-codes",
		"/api/v1/platform/requests",
	}
	perAgent := []string{
		"/api/v1/agents/" + a,
		"/api/v1/agents/" + a + "/config",
		"/api/v1/agents/" + a + "/export",
		"/api/v1/agents/" + a + "/diagnostics",
		"/api/v1/agents/" + a + "/lint",
		"/api/v1/agents/" + a + "/work-record",
		"/api/v1/agents/" + a + "/reviews",
		"/api/v1/agents/" + a + "/files",
		"/api/v1/agents/" + a + "/files/usage",
		"/api/v1/agents/" + a + "/heartbeats",
		"/api/v1/agents/" + a + "/backlog",
		"/api/v1/agents/" + a + "/home",
		"/api/v1/agents/" + a + "/placement",
		"/api/v1/agents/" + a + "/webhook",
		"/api/v1/agents/" + a + "/systems",
		"/api/v1/agents/" + a + "/recording",
		"/api/v1/agents/" + a + "/cost",
		"/api/v1/agents/" + a + "/cost/series",
		"/api/v1/agents/" + a + "/cost/runs",
		"/api/v1/agents/" + a + "/cost/indicators",
		"/api/v1/agents/" + a + "/memories",
		"/api/v1/agents/" + a + "/wiki/log",
		"/api/v1/agents/" + a + "/wiki/health",
		"/api/v1/agents/" + a + "/dreams",
		"/api/v1/agents/" + a + "/stages",
		"/api/v1/agents/" + a + "/egress",
		"/api/v1/agents/" + a + "/secrets",
		"/api/v1/agents/" + a + "/skills",
	}

	// Two of them answer 503 in this stack rather than 200, and that is the
	// correct answer: they need a part the test stack deliberately does not
	// wire (the workplace store, the process configuration). A 503 names a
	// missing dependency; what this test is looking for is a handler that
	// falls over on an EMPTY one.
	needsWiring := map[string]bool{
		"/api/v1/service-images":  true,
		"/api/v1/platform/doctor": true,
	}

	for _, path := range append(orgWide, perAgent...) {
		resp := admin.do(http.MethodGet, path, nil)
		var body any
		json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			continue
		}
		if needsWiring[path] && resp.StatusCode == http.StatusServiceUnavailable {
			continue
		}
		t.Errorf("GET %s: HTTP %d on a fresh installation (%v)", path, resp.StatusCode, body)
	}
}

// The same endpoints under an id that is not a uuid. Each has to say "bad
// request" rather than answering for an agent nobody named, or falling over.
func TestPerAgentEndpointsRefuseAnIdThatIsNotOne(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	for _, suffix := range []string{
		"", "/config", "/export", "/diagnostics", "/lint", "/work-record",
		"/files", "/files/usage", "/heartbeats", "/backlog", "/home", "/placement",
		"/webhook", "/systems", "/recording", "/cost", "/cost/series",
		"/memories", "/wiki/log", "/wiki/health", "/dreams", "/stages",
		"/egress", "/secrets", "/skills",
	} {
		path := "/api/v1/agents/keine-uuid" + suffix
		resp := admin.do(http.MethodGet, path, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: HTTP %d, expected 400 or 404", path, resp.StatusCode)
		}
	}
}

// A session is what the browser carries, and what a person can revoke. The
// endpoints around it are the ones somebody reaches for after a lost laptop.
func TestSessionsAndProfileOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	sessions := admin.expectList(http.MethodGet, "/api/v1/auth/sessions", nil, http.StatusOK)
	if len(sessions) == 0 {
		t.Fatal("the session this request is made with is not listed")
	}

	// The principal is serialised without json tags, so the fields keep their
	// Go names — worth pinning, because the frontend reads exactly this.
	me := admin.expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK)
	if me["Email"] != "admin@test.local" {
		t.Errorf("the principal names %v as the address", me["Email"])
	}
	if me["Role"] == nil || me["OrgID"] == nil {
		t.Errorf("the principal carries neither role nor organisation: %v", me)
	}
	if via, _ := me["ViaAPIKey"].(bool); via {
		t.Error("a browser session claims to be an API key")
	}

	updated := admin.expect(http.MethodPatch, "/api/v1/auth/me",
		map[string]any{"display_name": "Die Chefin"}, http.StatusOK)
	if updated["display_name"] != "Die Chefin" {
		t.Errorf("the change does not come back: %v", updated)
	}
	profile := admin.expect(http.MethodGet, "/api/v1/auth/me/profile", nil, http.StatusOK)
	if profile["display_name"] != "Die Chefin" {
		t.Errorf("the profile still says %v", profile["display_name"])
	}
	// A name that is nothing would leave the person nameless in the org chart.
	admin.expect(http.MethodPatch, "/api/v1/auth/me",
		map[string]any{"display_name": "   "}, http.StatusBadRequest)
	// The password changes only against proof of the current one.
	admin.expect(http.MethodPatch, "/api/v1/auth/me",
		map[string]any{"password": "ein-neues-langes", "current_password": "falsch"}, http.StatusForbidden)
	admin.expect(http.MethodPatch, "/api/v1/auth/me",
		map[string]any{"password": "kurz", "current_password": "admin-passwort"}, http.StatusBadRequest)

	// Notification settings are per person, not per organisation.
	admin.expect(http.MethodGet, "/api/v1/auth/notifications", nil, http.StatusOK)
}

// An API key is how covey is driven from outside. What it may do is narrower
// than what the browser may, and it carries its own listing.
func TestAPIKeysOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	made := admin.expect(http.MethodPost, "/api/v1/auth/api-keys",
		map[string]any{"name": "Für die Pipeline"}, http.StatusCreated)
	token, _ := made["token"].(string)
	if token == "" {
		t.Fatalf("the key was not handed out once: %v", made)
	}
	id, _ := made["id"].(string)

	list := admin.expectList(http.MethodGet, "/api/v1/auth/api-keys", nil, http.StatusOK)
	if len(list) == 0 {
		t.Fatal("the key is not in the listing")
	}
	// The listing never carries the token again — only its hash is stored.
	for _, k := range list {
		if v, _ := k["token"].(string); v != "" {
			t.Error("the listing hands the token out a second time")
		}
	}

	admin.expect(http.MethodDelete, "/api/v1/auth/api-keys/"+id, nil, http.StatusOK)
	admin.expect(http.MethodDelete, "/api/v1/auth/api-keys/keine-uuid", nil, http.StatusBadRequest)
}
