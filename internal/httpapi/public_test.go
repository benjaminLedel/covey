package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Ob eine Installation Registrierungen annimmt, ist die eine Frage, die die
// öffentliche Website ohne Sitzung stellt — und die einzige richtige Antwort
// im Zweifel ist "nein". Ohne Einstellungs-Store (Tests, ältere Montage) darf
// daraus kein offenes Formular werden.
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
	// Der Name steht auf der Registrierungsseite und später in den Mails; leer
	// wäre dort eine Lücke im Satz.
	if got.SiteName == "" {
		t.Error("site_name ist leer")
	}
	// Wer die Registrierung schließt, will sie jetzt geschlossen haben und
	// nicht, wenn die TTL eines Proxys abgelaufen ist.
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control=%q, erwartet no-store", cc)
	}
}
