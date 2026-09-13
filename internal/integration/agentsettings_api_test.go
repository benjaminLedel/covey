package integration

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// Everything an agent carries beside its config is set over these endpoints,
// and each of them refuses something on purpose. A value stored where it is
// not valid is a value that fails at the next wake, in a message pointing
// somewhere else.
func TestAgentSettingsOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("einstellungs-agent")
	base := "/api/v1/agents/" + agent.ID.String()

	// The effort level is the engine's own vocabulary — a level one engine
	// knows is a run-time error at the next, so it is checked against the
	// engine the agent runs on.
	admin.expect(http.MethodPatch, base+"/effort", map[string]string{"effort": ""}, http.StatusOK)
	admin.expect(http.MethodPatch, base+"/effort", map[string]string{"effort": "gemächlich"}, http.StatusBadRequest)

	// The recording level decides how much of a run is kept. It is a closed
	// vocabulary, because the interface has to be able to show it.
	for _, level := range []string{"minimal", "standard", "full", ""} {
		admin.expect(http.MethodPatch, base+"/recording-level", map[string]string{"level": level}, http.StatusOK)
	}
	admin.expect(http.MethodPatch, base+"/recording-level", map[string]string{"level": "alles"}, http.StatusBadRequest)

	// A retention of nothing is not the same as no retention: the pointer is
	// how "the organisation decides" is told apart from "this many days".
	admin.expect(http.MethodPatch, base+"/recording-retention", map[string]any{"retention_days": 7}, http.StatusOK)
	admin.expect(http.MethodPatch, base+"/recording-retention", map[string]any{"retention_days": nil}, http.StatusOK)

	// A turn limit is a lot size, and a negative one is none.
	admin.expect(http.MethodPatch, base+"/max-turns", map[string]any{"max_turns": 40}, http.StatusOK)
	admin.expect(http.MethodPatch, base+"/max-turns", map[string]any{"max_turns": 0}, http.StatusOK)
	admin.expect(http.MethodPatch, base+"/max-turns", map[string]any{"max_turns": -1}, http.StatusBadRequest)

	admin.expect(http.MethodPatch, base+"/warm-sandbox", map[string]any{"warm": true}, http.StatusOK)
	admin.expect(http.MethodPatch, base+"/warm-sandbox", map[string]any{"warm": false}, http.StatusOK)

	// Runner tags say what a host has to BE. An empty list is the normal case
	// and must stay settable — it is how a requirement is withdrawn.
	admin.expect(http.MethodPatch, base+"/runner-tags", map[string]any{"runner_tags": []string{"arm64", "gpu"}}, http.StatusOK)
	admin.expect(http.MethodPatch, base+"/runner-tags", map[string]any{"runner_tags": []string{}}, http.StatusOK)

	// A service beside the sandbox runs an image, and the organisation's
	// allowlist is the one thing between that reference and a container.
	admin.expect(http.MethodPatch, base+"/services", map[string]any{"services": []any{}}, http.StatusOK)
	admin.expect(http.MethodPatch, base+"/services", map[string]any{
		"services": []any{map[string]any{"name": "böse", "image": "attacker/backdoor:latest"}},
	}, http.StatusBadRequest)

	// The slug is an address. Two agents cannot share one.
	admin.expect(http.MethodPatch, base+"/slug", map[string]string{"slug": "neuer-slug"}, http.StatusOK)
	second := s.newSupportAgent("zweiter-agent")
	admin.expect(http.MethodPatch, "/api/v1/agents/"+second.ID.String()+"/slug",
		map[string]string{"slug": "neuer-slug"}, http.StatusConflict)

	// Every one of them refuses an id that is not one, rather than answering
	// for an agent nobody named.
	for _, path := range []string{"/effort", "/recording-level", "/max-turns", "/warm-sandbox", "/runner-tags", "/services", "/slug"} {
		admin.expect(http.MethodPatch, "/api/v1/agents/keine-uuid"+path, map[string]any{}, http.StatusBadRequest)
	}
}

// The recording level and its retention also exist for the whole organisation:
// what an agent does not decide for itself, the organisation decides.
func TestOrgRecordingOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	got := admin.expect(http.MethodGet, "/api/v1/org/recording-level", nil, http.StatusOK)
	if got == nil {
		t.Fatal("the organisation carries no recording level")
	}
	for _, level := range []string{"minimal", "standard", "full"} {
		admin.expect(http.MethodPatch, "/api/v1/org/recording-level", map[string]string{"level": level}, http.StatusOK)
	}
	// Unlike the agent's, the organisation's level has no "empty" — there is
	// nothing above it to fall back to.
	admin.expect(http.MethodPatch, "/api/v1/org/recording-level", map[string]string{"level": ""}, http.StatusBadRequest)
	admin.expect(http.MethodPatch, "/api/v1/org/recording-retention", map[string]any{"retention_days": 30}, http.StatusOK)
}

// Cost is counted per agent and per model, and the endpoints behind the tiles
// have to answer for an agent that has never run — a zero is an answer, an
// error is not.
func TestCostEndpoints(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("kosten-agent")
	base := "/api/v1/agents/" + agent.ID.String()

	admin.expect(http.MethodGet, base+"/cost", nil, http.StatusOK)
	admin.expect(http.MethodGet, base+"/cost/series", nil, http.StatusOK)
	admin.expect(http.MethodGet, base+"/cost/series?days=7", nil, http.StatusOK)
	admin.expect(http.MethodGet, "/api/v1/cost/org", nil, http.StatusOK)
	admin.expect(http.MethodGet, "/api/v1/agents/keine-uuid/cost", nil, http.StatusBadRequest)

	// The wiki health is the same kind of answer: a finding list, empty for a
	// memory nobody has written to.
	admin.expect(http.MethodGet, base+"/wiki/health", nil, http.StatusOK)

	admin.expectList(http.MethodGet, "/api/v1/guardrails", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/runtimes", nil, http.StatusOK)
}

// A secret is write-only from the API's side. What can be done with it —
// assign, mark, delete — happens by name, and every one of those has to refuse
// a key the organisation does not carry.
func TestSecretEndpoints(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("geheimnis-agent")

	admin.expect(http.MethodPut, "/api/v1/secrets/zammad_token",
		map[string]string{"value": "t-1"}, http.StatusOK)

	// Marking it sensitive is deliberately one-way: lifting the protection
	// again would mean disclosing the value after all.
	admin.expect(http.MethodPatch, "/api/v1/secrets/zammad_token",
		map[string]any{"sensitive": true}, http.StatusOK)

	// An org secret reaches an agent only on explicit assignment.
	admin.expect(http.MethodPut, "/api/v1/secrets/zammad_token/agents/"+agent.ID.String(), nil, http.StatusOK)
	list := admin.expectList(http.MethodGet, "/api/v1/agents/"+agent.ID.String()+"/secrets", nil, http.StatusOK)
	if list == nil {
		t.Fatal("the agent's secret list is not readable")
	}
	admin.expect(http.MethodDelete, "/api/v1/secrets/zammad_token/agents/"+agent.ID.String(), nil, http.StatusOK)
	admin.expect(http.MethodDelete, "/api/v1/secrets/zammad_token/agents/keine-uuid", nil, http.StatusBadRequest)

	// The agent's own secrets live beside the org's and are administered the
	// same way.
	admin.expect(http.MethodPut, "/api/v1/agents/"+agent.ID.String()+"/secrets/eigenes_token",
		map[string]string{"value": "nur-hier"}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/agents/"+agent.ID.String()+"/secrets/eigenes_token",
		map[string]any{"sensitive": true}, http.StatusOK)
	admin.expect(http.MethodDelete, "/api/v1/agents/"+agent.ID.String()+"/secrets/eigenes_token", nil, http.StatusOK)

	admin.expect(http.MethodDelete, "/api/v1/secrets/zammad_token", nil, http.StatusOK)
	// Deleting a secret is idempotent — a key that is not there answers ok,
	// not 404. Unlike the guard rails, whose store reports not-found on
	// purpose so that a foreign rule cannot be "deleted" with a confirmation
	// for something that never happened. Written down because the two read
	// differently at the same verb.
	admin.expect(http.MethodDelete, "/api/v1/secrets/gibtesnicht", nil, http.StatusOK)
	admin.expect(http.MethodGet, "/api/v1/agents/"+uuid.NewString()+"/secrets", nil, http.StatusNotFound)
}

// A secret's expiry is a date a PERSON knows — the token runs out at the end
// of the month, and the platform should say so before the run does. It is
// therefore taken in both the forms somebody types, and refused in the ones
// nobody means.
func TestSecretExpiryOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("ablauf-agent")

	admin.expect(http.MethodPut, "/api/v1/secrets/gitlab_token",
		map[string]string{"value": "glpat-x"}, http.StatusOK)

	// A plain date and a full timestamp both work: one is what a person reads
	// off a token page, the other what a machine writes.
	admin.expect(http.MethodPatch, "/api/v1/secrets/gitlab_token",
		map[string]any{"expires_at": "2026-12-31"}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/secrets/gitlab_token",
		map[string]any{"expires_at": "2026-12-31T23:59:00Z"}, http.StatusOK)

	// Clearing it: both spellings of "no longer known".
	admin.expect(http.MethodPatch, "/api/v1/secrets/gitlab_token",
		map[string]any{"expires_at": nil}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/secrets/gitlab_token",
		map[string]any{"expires_at": ""}, http.StatusOK)

	// A date nobody can mean is refused with the shape it wanted, rather than
	// stored as a zero time that would read as "ran out in year one".
	admin.expect(http.MethodPatch, "/api/v1/secrets/gitlab_token",
		map[string]any{"expires_at": "31.12.2026"}, http.StatusBadRequest)
	admin.expect(http.MethodPatch, "/api/v1/secrets/gitlab_token",
		map[string]any{"expires_at": "bald"}, http.StatusBadRequest)

	// A PATCH that changes nothing is a bad request: it would otherwise report
	// success for an operation nobody asked for.
	admin.expect(http.MethodPatch, "/api/v1/secrets/gitlab_token",
		map[string]any{}, http.StatusBadRequest)

	// The protection is one-way. Lifting it would mean disclosing the value
	// after all, so the way back is delete and create anew.
	admin.expect(http.MethodPatch, "/api/v1/secrets/gitlab_token",
		map[string]any{"sensitive": true}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/secrets/gitlab_token",
		map[string]any{"sensitive": false}, http.StatusConflict)

	// The agent's own secrets take the same patches.
	base := "/api/v1/agents/" + agent.ID.String() + "/secrets/eigenes"
	admin.expect(http.MethodPut, base, map[string]string{"value": "nur-hier"}, http.StatusOK)
	admin.expect(http.MethodPatch, base, map[string]any{"expires_at": "2027-01-31"}, http.StatusOK)
	admin.expect(http.MethodPatch, base, map[string]any{"expires_at": "irgendwann"}, http.StatusBadRequest)
	admin.expect(http.MethodPatch, base, map[string]any{"sensitive": false}, http.StatusConflict)
	admin.expect(http.MethodPatch, base, map[string]any{}, http.StatusBadRequest)

	// What the listing shows: names, and what is known about the value's life —
	// never the value.
	list := admin.expectList(http.MethodGet, "/api/v1/secrets", nil, http.StatusOK)
	if len(list) == 0 {
		t.Fatal("the secret is not in the listing")
	}
	for _, e := range list {
		if v, _ := e["value"].(string); v != "" {
			t.Error("the listing hands a secret's value out")
		}
	}
}

// The fleet view is the one page that answers "what is everybody doing right
// now". On a fresh installation that is nobody, and it still has to answer.
func TestFleetStatusOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("flotten-agent")

	fleet := admin.expect(http.MethodGet, "/api/v1/fleet", nil, http.StatusOK)
	if fleet == nil {
		t.Fatal("the fleet view answered nothing")
	}

	// The kill switch is the whole organisation at once, and the fleet view is
	// where it is read back.
	admin.expect(http.MethodPost, "/api/v1/fleet/kill", map[string]any{"killed": true}, http.StatusOK)
	after := admin.expect(http.MethodGet, "/api/v1/fleet", nil, http.StatusOK)
	if killed, _ := after["fleet_killed"].(bool); !killed {
		t.Errorf("the kill switch is not read back: %v", after)
	}
	admin.expect(http.MethodPost, "/api/v1/fleet/kill", map[string]any{"killed": false}, http.StatusOK)
	_ = agent
}
