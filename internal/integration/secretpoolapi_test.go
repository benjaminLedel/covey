package integration

import (
	"context"
	"net/http"
	"testing"

	"covey/internal/observability"
)

// TestSecretPoolAPI drives the pool over HTTP: create, look at, limit, rename,
// remove. Alongside the happy path it pins the two refusals, because both
// protect against a state the store cannot repair afterwards — a secret without
// any value, and a cooldown claimed by hand without a measurement behind it.
func TestSecretPoolAPI(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("alice")

	const key = "claude_code_oauth_token"
	admin.expect(http.MethodPut, "/api/v1/secrets/"+key,
		map[string]any{"value": "wert-eins-lang-genug", "sensitive": true}, http.StatusOK)

	// A second and third value under the same key.
	first := admin.expect(http.MethodPost, "/api/v1/secrets/"+key+"/values",
		map[string]any{"value": "wert-zwei-lang-genug", "label": "Abo B"}, http.StatusOK)
	if first["slot"] != float64(1) {
		t.Fatalf("the second value has to become slot 1: %v", first["slot"])
	}
	second := admin.expect(http.MethodPost, "/api/v1/secrets/"+key+"/values",
		map[string]any{"value": "wert-drei-lang-genug", "label": "Abo C"}, http.StatusOK)
	if second["slot"] != float64(2) {
		t.Fatalf("the third value has to become slot 2: %v", second["slot"])
	}

	// The list carries the pool, and the added values inherit the protection of
	// the key. A pool half readable and half write-only would be a trap.
	list := admin.expectList(http.MethodGet, "/api/v1/secrets", nil, http.StatusOK)
	var found map[string]any
	for _, entry := range list {
		if entry["key"] == key {
			found = entry
		}
	}
	if found == nil {
		t.Fatal("the key is missing from the list")
	}
	values, _ := found["values"].([]any)
	if len(values) != 3 {
		t.Fatalf("the pool has to carry three values: %v", found["values"])
	}
	for _, raw := range values {
		v := raw.(map[string]any)
		if v["sensitive"] != true {
			t.Fatalf("added value not protected: %v", v)
		}
		if v["value"] != nil {
			t.Fatalf("a sensitive value must not be readable: %v", v)
		}
	}

	// Limit and label.
	admin.expect(http.MethodPatch, "/api/v1/secrets/"+key+"/values/1",
		map[string]any{"limit": map[string]any{"amount": 5, "unit": "usd", "window_secs": 18000}}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/secrets/"+key+"/values/2",
		map[string]any{"label": "Abo C (Reserve)"}, http.StatusOK)

	// Consumption is reported per value, and each over ITS OWN window: slot 1
	// carries a limit over 5 h, the others get the display window. Booked
	// exactly the way a run books it.
	if err := s.obs.AddCost(ctx, agent.ID, nil, 2.50, observability.Tokens{Input: 100, Output: 50},
		"claude-opus-5", key, 1); err != nil {
		t.Fatal(err)
	}
	pool := admin.expect(http.MethodGet, "/api/v1/secrets/"+key+"/pool", nil, http.StatusOK)
	poolValues, _ := pool["values"].([]any)
	if len(poolValues) != 3 {
		t.Fatalf("pool view: %v", pool["values"])
	}
	byslot := map[float64]map[string]any{}
	for _, raw := range poolValues {
		v := raw.(map[string]any)
		byslot[v["slot"].(float64)] = v
	}
	if got := byslot[1]["window_secs"]; got != float64(18000) {
		t.Fatalf("a value with a limit is measured over its own window: %v", got)
	}
	if got := byslot[0]["window_secs"]; got != float64(86400) {
		t.Fatalf("a value without a limit gets the display window: %v", got)
	}
	if got := byslot[1]["usage"].(map[string]any)["usd"]; got != 2.5 {
		t.Fatalf("the consumption has to land on slot 1: %v", got)
	}
	if got := byslot[0]["usage"].(map[string]any)["usd"]; got != float64(0) {
		t.Fatalf("slot 0 has consumed nothing: %v", got)
	}
	if got := byslot[2]["label"]; got != "Abo C (Reserve)" {
		t.Fatalf("rename did not take: %v", got)
	}

	// A cooldown is a claim about a measurement — it is set by the platform, not
	// by hand. Lifting one is allowed; that is the "try it again" case.
	admin.expect(http.MethodPatch, "/api/v1/secrets/"+key+"/values/2",
		map[string]any{"cooldown": true}, http.StatusBadRequest)
	admin.expect(http.MethodPatch, "/api/v1/secrets/"+key+"/values/2",
		map[string]any{"cooldown": false}, http.StatusOK)

	// Removing values: down to the last one, and that one only with the key.
	// Otherwise a secret would be left standing with no value at all — not
	// gone, but unusable, and a state nothing else in the store expects.
	admin.expect(http.MethodDelete, "/api/v1/secrets/"+key+"/values/2", nil, http.StatusOK)
	admin.expect(http.MethodDelete, "/api/v1/secrets/"+key+"/values/1", nil, http.StatusOK)
	admin.expect(http.MethodDelete, "/api/v1/secrets/"+key+"/values/0", nil, http.StatusConflict)
	admin.expect(http.MethodDelete, "/api/v1/secrets/"+key, nil, http.StatusOK)

	if got := admin.do(http.MethodGet, "/api/v1/secrets/"+key+"/pool", nil); got.StatusCode != http.StatusNotFound {
		got.Body.Close()
		t.Fatalf("the pool of a deleted key: HTTP %d", got.StatusCode)
	}
}

// TestSecretPoolAPIRefusals: a pool grows out of an existing secret, and only
// roles that may see the secret store may touch it.
func TestSecretPoolAPIRefusals(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// Without the key there is nothing to append to — creating one here would
	// bypass PUT and its live check.
	admin.expect(http.MethodPost, "/api/v1/secrets/gibtsnicht/values",
		map[string]any{"value": "wert"}, http.StatusNotFound)
	admin.expect(http.MethodGet, "/api/v1/secrets/gibtsnicht/pool", nil, http.StatusNotFound)

	// A slot that is not a number must not reach the store.
	admin.expect(http.MethodPut, "/api/v1/secrets/zammad_token",
		map[string]any{"value": "org-token"}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/secrets/zammad_token/values/keine-zahl",
		map[string]any{"label": "x"}, http.StatusBadRequest)
	admin.expect(http.MethodDelete, "/api/v1/secrets/zammad_token/values/keine-zahl", nil, http.StatusBadRequest)

	// An unknown unit is refused — otherwise a limit would stand that nothing
	// ever measures against.
	admin.expect(http.MethodPatch, "/api/v1/secrets/zammad_token/values/0",
		map[string]any{"limit": map[string]any{"amount": 5, "unit": "bananen", "window_secs": 3600}},
		http.StatusBadRequest)

	// A slot that is a number but does not exist has to be answered as missing,
	// for each of the three things a PATCH can change. Silently doing nothing
	// and reporting success is the failure mode worth guarding: the interface
	// would show the old limit and everyone would assume the new one applies.
	for _, body := range []map[string]any{
		{"label": "x"},
		{"limit": map[string]any{"amount": 5, "unit": "usd", "window_secs": 3600}},
		{"cooldown": false},
	} {
		admin.expect(http.MethodPatch, "/api/v1/secrets/zammad_token/values/9", body, http.StatusNotFound)
	}
	admin.expect(http.MethodDelete, "/api/v1/secrets/zammad_token/values/9", nil, http.StatusNotFound)

	// A malformed body is a bad request, not a server fault.
	resp := admin.do(http.MethodPatch, "/api/v1/secrets/zammad_token/values/0", "kein objekt")
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed PATCH body: HTTP %d", resp.StatusCode)
	}
	// An added value without one is likewise.
	admin.expect(http.MethodPost, "/api/v1/secrets/zammad_token/values",
		map[string]any{"label": "ohne Wert"}, http.StatusBadRequest)
}
