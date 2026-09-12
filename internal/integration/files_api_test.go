package integration

import (
	"net/http"
	"testing"
)

// The workspace is an agent's home directory in the browser: readable while
// the agent sleeps, and writable — which is why every path that leaves it has
// to be refused here rather than inside the sandbox.
func TestWorkspaceFilesOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("dateien-agent")
	base := "/api/v1/agents/" + agent.ID.String() + "/files"

	admin.expect(http.MethodGet, base, nil, http.StatusOK)
	admin.expect(http.MethodGet, base+"/usage", nil, http.StatusOK)

	admin.expect(http.MethodPost, base+"/dir", map[string]string{"path": "notizen"}, http.StatusCreated)
	admin.expect(http.MethodPut, base+"/content",
		map[string]string{"path": "notizen/eins.md", "content": "# Eins\n"}, http.StatusOK)

	got := admin.expect(http.MethodGet, base+"/content?path=notizen/eins.md", nil, http.StatusOK)
	if content, _ := got["content"].(string); content != "# Eins\n" {
		t.Errorf("the file came back as %q", content)
	}

	admin.expect(http.MethodPost, base+"/move",
		map[string]string{"from": "notizen/eins.md", "to": "notizen/zwei.md"}, http.StatusOK)
	admin.expect(http.MethodGet, base+"/content?path=notizen/zwei.md", nil, http.StatusOK)

	// A path that tries to climb out is not refused — it is CLAMPED. Every
	// `..` falls away on the detour through "/", and the write then lands
	// inside the home under the remaining name. That is the deliberate shape:
	// as long as the check was a separate step there was a window between
	// checking and opening, and the agent has a shell in the home and could
	// swap a symlink into it. The guarantee to test is therefore not a 400,
	// it is WHERE the file ends up.
	for _, climb := range []struct{ path, lands string }{
		{"../draussen.md", "draussen.md"},
		{"/etc/passwd", "etc/passwd"},
		{"notizen/../../weg.md", "weg.md"},
	} {
		got := admin.expect(http.MethodPut, base+"/content",
			map[string]string{"path": climb.path, "content": "x"}, http.StatusOK)
		if got["path"] != climb.lands {
			t.Errorf("%q landed at %v, expected %q inside the home", climb.path, got["path"], climb.lands)
		}
	}
	// And reading follows the same clamp, so the two cannot disagree about
	// which file a path means.
	admin.expect(http.MethodGet, base+"/content?path=../draussen.md", nil, http.StatusOK)
	admin.expect(http.MethodGet, base+"/content?path=gibtesnicht.md", nil, http.StatusNotFound)

	admin.expect(http.MethodDelete, base+"?path=notizen/zwei.md", nil, http.StatusOK)
}

// The heartbeat is what wakes an agent on a schedule. Firing one by hand is
// how somebody checks that it does what it says.
func TestHeartbeatsOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("takt-agent")
	base := "/api/v1/agents/" + agent.ID.String()

	admin.expectList(http.MethodGet, base+"/heartbeats", nil, http.StatusOK)
	// A heartbeat this agent does not declare cannot be fired — the name comes
	// out of its own HEARTBEAT.md.
	admin.expect(http.MethodPost, base+"/heartbeats/gibtesnicht/fire", nil, http.StatusNotFound)
	admin.expect(http.MethodGet, "/api/v1/agents/keine-uuid/heartbeats", nil, http.StatusBadRequest)
}

// The org-wide views: the fleet, the audit trail, the event stream's backlog.
// Each is a read somebody opens when something has gone wrong, so each has to
// answer when nothing has.
func TestOrgWideViewsOverTheAPI(t *testing.T) {
	s := newStack(t)
	s.alsSystemadmin(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	admin.expect(http.MethodGet, "/api/v1/fleet", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/audit", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/audit?limit=5", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/approvals", nil, http.StatusOK)
	admin.expect(http.MethodGet, "/api/v1/inbox", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/improvements", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/guardrails/events", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/platform/requests", nil, http.StatusOK)
	admin.expect(http.MethodGet, "/api/v1/platform/settings", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/platform/orgs", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/platform/accounts", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/platform/waitlist-codes", nil, http.StatusOK)
}
