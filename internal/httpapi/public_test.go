package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Whether an installation accepts registrations is the one question the
// public website asks without a session — and the only right answer
// when in doubt is "no". Without a settings store (tests, older
// wiring) that must not turn into an open form.
func TestSignupStateOhneStoreIstGeschlossen(t *testing.T) {
	s := &Server{} // Settings == nil
	rec := httptest.NewRecorder()
	s.handleSignupState(rec, httptest.NewRequest(http.MethodGet, "/api/v1/public/signup-state", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("Status %d, erwartet 200", rec.Code)
	}
	var got signupState
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Antwort nicht lesbar: %v", err)
	}
	if got.Mode != "off" {
		t.Errorf("mode=%q, erwartet \"off\" — fail-closed", got.Mode)
	}
	// The name stands on the registration page and later in the mails; empty
	// would be a hole in the sentence there.
	if got.SiteName == "" {
		t.Error("site_name ist leer")
	}
	// Whoever closes registration wants it closed now and
	// not once a proxy's TTL has run out.
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control=%q, erwartet no-store", cc)
	}
}
