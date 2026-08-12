package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestSetRuntimeFallbackRefusesSelfReference pins a guard against a fallback
// chain of length one: a runtime pointing at itself would make the
// exhaustion path (fallbackCredential in the orchestrator) pick the SAME
// exhausted runtime again and report exhaustion right back, silently, instead
// of the plain wait that no-fallback-configured already produces correctly.
// The database also refuses this (CHECK runtimes_fallback_not_self), but the
// rejection has to happen here, with a readable message, before ever reaching
// the store — a bad id there is a generic "not found", not "cannot be its own
// fallback".
func TestSetRuntimeFallbackRefusesSelfReference(t *testing.T) {
	s := &Server{}
	id := uuid.New()
	body := `{"fallback_runtime_id":"` + id.String() + `"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/runtime-instances/"+id.String()+"/fallback",
		strings.NewReader(body))
	req.SetPathValue("id", id.String())
	rec := httptest.NewRecorder()

	s.handleSetRuntimeFallback(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "own fallback") {
		t.Errorf("body = %q, want an explanation naming the self-reference", rec.Body.String())
	}
}

// TestSetRuntimeFallbackRejectsInvalidID: a malformed path id must not panic
// or reach the store with a zero uuid.
func TestSetRuntimeFallbackRejectsInvalidID(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/runtime-instances/not-a-uuid/fallback",
		strings.NewReader(`{"fallback_runtime_id":null}`))
	req.SetPathValue("id", "not-a-uuid")
	rec := httptest.NewRecorder()

	s.handleSetRuntimeFallback(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
