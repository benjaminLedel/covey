package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// A seat is where an organisation's model capacity is administered. The store
// behind it is well covered (runtimes_test.go); these are the endpoints, which
// is where somebody actually enters the thing — and where a refusal has to
// arrive before a wake turns it into a wrong token.
func TestRuntimeInstancesOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// The engines the binary carries. It is the list the interface offers, so
	// an empty one would be a setup with nothing to pick.
	engines := admin.expectList(http.MethodGet, "/api/v1/runtimes", nil, http.StatusOK)
	if len(engines) == 0 {
		t.Fatal("no engine is offered")
	}

	rt := admin.expect(http.MethodPost, "/api/v1/runtime-instances",
		map[string]string{"engine": "mock", "display_name": "Mock-Sitz"}, http.StatusOK)
	id, _ := rt["id"].(string)
	if id == "" {
		t.Fatalf("the seat carries no id: %v", rt)
	}

	// Engine and name are both required, and an engine this binary does not
	// carry is refused by name rather than stored and failed on at the wake.
	admin.expect(http.MethodPost, "/api/v1/runtime-instances",
		map[string]string{"display_name": "ohne Engine"}, http.StatusBadRequest)
	admin.expect(http.MethodPost, "/api/v1/runtime-instances",
		map[string]string{"engine": "mock"}, http.StatusBadRequest)
	admin.expect(http.MethodPost, "/api/v1/runtime-instances",
		map[string]string{"engine": "gibtesnicht", "display_name": "X"}, http.StatusBadRequest)

	list := admin.expectList(http.MethodGet, "/api/v1/runtime-instances", nil, http.StatusOK)
	if len(list) == 0 {
		t.Fatal("the seat is not in the list")
	}

	admin.expect(http.MethodPut, "/api/v1/runtime-instances/"+id,
		map[string]string{"display_name": "Mock-Sitz (umbenannt)"}, http.StatusOK)
	admin.expect(http.MethodPut, "/api/v1/runtime-instances/"+id,
		map[string]string{"display_name": ""}, http.StatusBadRequest)
	admin.expect(http.MethodPut, "/api/v1/runtime-instances/keine-uuid",
		map[string]string{"display_name": "X"}, http.StatusBadRequest)

	// A seat that does not exist is not found — not a server error. Its store
	// used to fall through to the default branch (#235).
	admin.expect(http.MethodPut, "/api/v1/runtime-instances/"+uuid.NewString(),
		map[string]string{"display_name": "X"}, http.StatusNotFound)
	admin.expect(http.MethodDelete, "/api/v1/runtime-instances/"+uuid.NewString(), nil, http.StatusNotFound)

	admin.expect(http.MethodDelete, "/api/v1/runtime-instances/"+id, nil, http.StatusOK)
	admin.expect(http.MethodDelete, "/api/v1/runtime-instances/keine-uuid", nil, http.StatusBadRequest)
}

// The credentials of a seat ARE the capacity. Their order is the merit order —
// a statement somebody makes on purpose, which is why it has its own endpoint.
func TestRuntimeCredentialsOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	ctx := context.Background()

	rt := admin.expect(http.MethodPost, "/api/v1/runtime-instances",
		map[string]string{"engine": "claude-code", "display_name": "Claude"}, http.StatusOK)
	id := rt["id"].(string)

	if err := s.secrets.Put(ctx, s.orgID, "anthropic_api_key", "sk-ant-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.Put(ctx, s.orgID, "claude_code_oauth_token", "oauth-1"); err != nil {
		t.Fatal(err)
	}

	first := admin.expect(http.MethodPost, "/api/v1/runtime-instances/"+id+"/credentials",
		map[string]any{"kind": "subscription", "secret_key": "claude_code_oauth_token", "label": "Abo"},
		http.StatusOK)
	second := admin.expect(http.MethodPost, "/api/v1/runtime-instances/"+id+"/credentials",
		map[string]any{"kind": "api_key", "secret_key": "anthropic_api_key", "label": "Schlüssel"},
		http.StatusOK)
	ord1, ok1 := first["ord"].(float64)
	ord2, ok2 := second["ord"].(float64)
	if !ok1 || !ok2 || ord1 == ord2 {
		t.Fatalf("the two credentials share a place: %v / %v", first, second)
	}

	admin.expect(http.MethodPost, "/api/v1/runtime-instances/"+id+"/credentials",
		map[string]any{"secret_key": "anthropic_api_key"}, http.StatusBadRequest)
	admin.expect(http.MethodPost, "/api/v1/runtime-instances/"+id+"/credentials",
		map[string]any{"kind": "api_key"}, http.StatusBadRequest)

	// Paid-for capacity first, metered for the peak — that is what the order
	// endpoint is for.
	admin.expect(http.MethodPost, "/api/v1/runtime-instances/"+id+"/credentials/order",
		map[string]any{"order": []int{int(ord2), int(ord1)}}, http.StatusOK)
	admin.expect(http.MethodPost, "/api/v1/runtime-instances/"+id+"/credentials/order",
		map[string]any{"order": []int{}}, http.StatusBadRequest)

	base := "/api/v1/runtime-instances/" + id + "/credentials/"
	admin.expect(http.MethodPatch, base+"0", map[string]any{"label": "neuer Name"}, http.StatusOK)
	admin.expect(http.MethodPatch, base+"0",
		map[string]any{"limit": map[string]any{"amount": 10, "unit": "usd", "window_secs": 3600}}, http.StatusOK)
	// The unit decides what a limit MEANS; an unknown one would be a limit
	// nobody can read.
	admin.expect(http.MethodPatch, base+"0",
		map[string]any{"limit": map[string]any{"amount": 10, "unit": "euro", "window_secs": 3600}}, http.StatusBadRequest)
	// A park may be lifted by hand; setting one claims a measurement and
	// belongs to the platform.
	admin.expect(http.MethodPatch, base+"0", map[string]any{"cooldown": false}, http.StatusOK)
	admin.expect(http.MethodPatch, base+"0", map[string]any{"cooldown": true}, http.StatusBadRequest)
	admin.expect(http.MethodPatch, base+"keine-zahl", map[string]any{"label": "x"}, http.StatusBadRequest)

	admin.expect(http.MethodDelete, base+"1", nil, http.StatusOK)
	admin.expect(http.MethodDelete, base+"keine-zahl", nil, http.StatusBadRequest)
}

// Putting an agent on a seat of another engine does not produce a wrong error
// later, it produces a wrong TOKEN — so it is refused here, where somebody is
// looking.
func TestAssignRuntimeOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("seat-agent")

	mock := admin.expect(http.MethodPost, "/api/v1/runtime-instances",
		map[string]string{"engine": "mock", "display_name": "Mock"}, http.StatusOK)
	claude := admin.expect(http.MethodPost, "/api/v1/runtime-instances",
		map[string]string{"engine": "claude-code", "display_name": "Claude"}, http.StatusOK)

	path := "/api/v1/agents/" + agent.ID.String() + "/runtime-instance"
	admin.expect(http.MethodPost, path, map[string]string{"runtime_id": mock["id"].(string)}, http.StatusOK)

	// The agent runs on the mock engine; the Claude seat carries Claude's
	// credentials and is refused with both names in the message.
	admin.expect(http.MethodPost, path, map[string]string{"runtime_id": claude["id"].(string)}, http.StatusBadRequest)
	admin.expect(http.MethodPost, path, map[string]string{"runtime_id": "keine-uuid"}, http.StatusBadRequest)
}

// A guard rail that does not exist is not found either — same finding, same
// place (#235).
func TestDeletingAGuardrailThatIsNotThereIsNotFound(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodDelete, "/api/v1/guardrails/"+uuid.NewString(), nil, http.StatusNotFound)
	admin.expect(http.MethodDelete, "/api/v1/guardrails/keine-uuid", nil, http.StatusBadRequest)
}
