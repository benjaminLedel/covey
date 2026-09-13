package integration

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// A runner is a machine somebody put covey on. The endpoints around it are how
// it is taken into service, told what it is, and taken out again — and every
// one of them is reached from a browser by a person who cannot see that host.
func TestRegistrationTokensOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	made := admin.expect(http.MethodPost, "/api/v1/runners/registration-tokens",
		map[string]string{"description": "der Rechner im Keller"}, http.StatusOK)
	token, _ := made["token"].(string)
	if token == "" {
		t.Fatalf("the registration token was not handed out: %v", made)
	}

	list := admin.expectList(http.MethodGet, "/api/v1/runners/registration-tokens", nil, http.StatusOK)
	if len(list) == 0 {
		t.Fatal("the token is not in the list")
	}
	// The listing never carries the token again — only its hash is kept, and a
	// registration token is what turns a machine into part of the data plane.
	var id string
	for _, e := range list {
		if v, _ := e["token"].(string); v != "" {
			t.Error("the listing hands the token out a second time")
		}
		if id == "" {
			id, _ = e["id"].(string)
		}
	}
	if id == "" {
		t.Fatalf("the token carries no id: %v", list)
	}

	admin.expect(http.MethodPost, "/api/v1/runners/registration-tokens/"+id+"/revoke", nil, http.StatusOK)
	admin.expect(http.MethodPost, "/api/v1/runners/registration-tokens/keine-uuid/revoke", nil, http.StatusBadRequest)
}

// What a runner IS — its tags, the images it holds, whether it is paused — is
// set from here. A pause takes a host out of service without taking it apart:
// it keeps its token, its tags and its working copies.
func TestRunnerAdministrationOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	rn, _ := s.builtinToken(t, s.orgID)
	id := rn.ID.String()

	admin.expect(http.MethodPatch, "/api/v1/runners/"+id,
		map[string]any{"name": "Keller", "description": "unter der Treppe"}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/runners/"+id,
		map[string]any{"tags": []string{"arm64", "gpu"}}, http.StatusOK)
	// An empty list is how a requirement is withdrawn, and it has to be told
	// apart from "not mentioned" — hence the pointer in the request.
	admin.expect(http.MethodPatch, "/api/v1/runners/"+id,
		map[string]any{"tags": []string{}}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/runners/"+id,
		map[string]any{"paused": true}, http.StatusOK)

	list := admin.expectList(http.MethodGet, "/api/v1/runners", nil, http.StatusOK)
	var found bool
	for _, r := range list {
		if r["id"] == id {
			found = true
			// A pause is a moment, not a flag: WHEN somebody took the host out
			// of service is what an operator reads two weeks later.
			if r["paused_at"] == nil {
				t.Errorf("the pause did not take: %v", r)
			}
			if r["name"] != "Keller" {
				t.Errorf("the name is %v", r["name"])
			}
		}
	}
	if !found {
		t.Errorf("the runner is not in the list: %v", list)
	}

	admin.expect(http.MethodPatch, "/api/v1/runners/"+id,
		map[string]any{"paused": false}, http.StatusOK)

	admin.expect(http.MethodGet, "/api/v1/runners/health", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/runners/"+id+"/logs", nil, http.StatusOK)

	admin.expect(http.MethodPatch, "/api/v1/runners/keine-uuid", map[string]any{}, http.StatusBadRequest)
	admin.expect(http.MethodPatch, "/api/v1/runners/"+uuid.NewString(), map[string]any{"paused": true}, http.StatusNotFound)
	admin.expect(http.MethodGet, "/api/v1/runners/keine-uuid/logs", nil, http.StatusBadRequest)

	// A pull needs to know WHAT to pull; an empty body would be a job nobody
	// can carry out.
	admin.expect(http.MethodPost, "/api/v1/runners/"+id+"/pull", map[string]any{}, http.StatusBadRequest)
	admin.expect(http.MethodPost, "/api/v1/runners/keine-uuid/pull",
		map[string]any{"image": "covey-sandbox:test"}, http.StatusBadRequest)

	// The built-in runner is not deletable: it is this control plane's own
	// machine, and deleting it would be deleting the instance's data plane
	// while the instance is what answers the request. Only a remote host can
	// go, which is why the store's DELETE is scoped to kind='remote'.
	admin.expect(http.MethodDelete, "/api/v1/runners/"+id, nil, http.StatusNotFound)
	admin.expect(http.MethodDelete, "/api/v1/runners/keine-uuid", nil, http.StatusBadRequest)
}

// A planned update waits for the host to be idle. Cancelling one is the other
// half — an operator who changed their mind must not have to wait for a
// sandbox to finish to take it back.
func TestPlannedRunnerUpdateOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	rn, _ := s.builtinToken(t, s.orgID)
	id := rn.ID.String()

	admin.expect(http.MethodDelete, "/api/v1/runners/"+id+"/update", nil, http.StatusOK)
	admin.expect(http.MethodPost, "/api/v1/runners/keine-uuid/update",
		map[string]string{"version": "v0.9.0"}, http.StatusBadRequest)
	admin.expect(http.MethodDelete, "/api/v1/runners/keine-uuid/update", nil, http.StatusBadRequest)
}

// The workplace catalogue: which image an agent works in. An organisation can
// add one of its own, and the name is how it is addressed everywhere else.
func TestWorkplacesOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	list := admin.expectList(http.MethodGet, "/api/v1/workplaces", nil, http.StatusOK)
	if len(list) == 0 {
		t.Fatal("no workplace is offered — an agent would have nowhere to run")
	}
	for _, w := range list {
		if w["name"] == "" || w["image"] == "" {
			t.Errorf("a workplace without a name or an image: %v", w)
		}
	}
}

// An organisation can bring a workplace of its own — an image an agent works
// in that the published catalogue does not carry. The one rule that has to
// hold is the name: a published name cannot be taken, because which image a
// name means must not depend on who looks first.
func TestOwnWorkplacesOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	before := admin.expectList(http.MethodGet, "/api/v1/workplaces", nil, http.StatusOK)
	if len(before) == 0 {
		t.Fatal("no workplace at all is offered")
	}
	var published string
	for _, w := range before {
		if name, _ := w["name"].(string); name != "" {
			published = name
			break
		}
	}

	made := admin.expect(http.MethodPost, "/api/v1/workplaces", map[string]any{
		"name": "hausbau", "label": "Hausbau", "description": "mit unserem Werkzeug",
		"image": "registry.example/hausbau:1",
	}, http.StatusCreated)
	if made["name"] != "hausbau" {
		t.Fatalf("the workplace was created as %v", made)
	}

	after := admin.expectList(http.MethodGet, "/api/v1/workplaces", nil, http.StatusOK)
	if len(after) != len(before)+1 {
		t.Errorf("the list holds %d workplaces, expected one more than %d", len(after), len(before))
	}

	// A published name cannot be claimed: the resolution prefers the published
	// image, so an own workplace of that name would be an entry that never
	// applies — refused where it is entered rather than discovered later.
	if published != "" {
		admin.expect(http.MethodPost, "/api/v1/workplaces",
			map[string]any{"name": published, "image": "registry.example/andere:1"}, http.StatusConflict)
	}
	admin.expect(http.MethodPost, "/api/v1/workplaces",
		map[string]any{"name": "ohne-bild"}, http.StatusBadRequest)

	admin.expect(http.MethodDelete, "/api/v1/workplaces/hausbau", nil, http.StatusOK)
	admin.expect(http.MethodDelete, "/api/v1/workplaces/gibtesnicht", nil, http.StatusNotFound)
}

// The service-image allowlist is the one thing standing between an image
// reference in an agent's config and a container on a runner. It is
// administered here, and the syntax is checked here rather than trusted.
func TestServiceImageAllowlistOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	if list := admin.expectList(http.MethodGet, "/api/v1/service-images", nil, http.StatusOK); len(list) != 0 {
		t.Errorf("a fresh organisation already allows service images: %v", list)
	}

	made := admin.expect(http.MethodPost, "/api/v1/service-images",
		map[string]string{"pattern": "postgres:*", "note": "für Projektdatenbanken"}, http.StatusCreated)
	id, _ := made["id"].(string)
	if id == "" {
		t.Fatalf("the pattern carries no id: %v", made)
	}

	// A pattern that cannot mean what its author thinks is refused where
	// somebody is looking. `postgres*` would also match
	// postgres-evil.example.com/backdoor, and nobody reading the list would
	// see it — so the star has to follow a separator, and it has to stand at
	// the end.
	for _, bad := range []string{"postgres*", "post*gres:16", "mit leerzeichen", ""} {
		admin.expect(http.MethodPost, "/api/v1/service-images",
			map[string]string{"pattern": bad}, http.StatusBadRequest)
	}

	list := admin.expectList(http.MethodGet, "/api/v1/service-images", nil, http.StatusOK)
	if len(list) != 1 {
		t.Fatalf("the allowlist holds %d entries", len(list))
	}

	// And with the pattern in place a service the agent declares is accepted,
	// where an image outside it is not.
	agent := s.newSupportAgent("dienst-agent")
	base := "/api/v1/agents/" + agent.ID.String() + "/services"
	admin.expect(http.MethodPatch, base, map[string]any{
		"services": []any{map[string]any{"name": "db", "image": "postgres:16"}},
	}, http.StatusOK)
	admin.expect(http.MethodPatch, base, map[string]any{
		"services": []any{map[string]any{"name": "böse", "image": "attacker/backdoor:latest"}},
	}, http.StatusBadRequest)

	admin.expect(http.MethodDelete, "/api/v1/service-images/"+id, nil, http.StatusOK)
	admin.expect(http.MethodDelete, "/api/v1/service-images/keine-uuid", nil, http.StatusBadRequest)
}
