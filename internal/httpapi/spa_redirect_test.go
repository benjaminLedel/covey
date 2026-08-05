package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// Der Trailing-Slash-Redirect baut sein Ziel aus dem Anfragepfad. Bleibt der
// nicht lokal, ist es ein Open Redirect: `//evil.com/` würde zu `//evil.com`,
// und das liest ein Browser als protokoll-relative Adresse auf einen fremden
// Host.
//
// In Produktion normalisiert der ServeMux das vorher weg — aber das ist eine
// Eigenschaft der Montage, nicht des Handlers. Der Test greift deshalb den
// Handler DIREKT an, sonst prüfte er den Mux statt den Fix.
func TestSPARedirectBleibtLokal(t *testing.T) {
	s := &Server{WebFS: fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html></html>")},
	}}
	handler := s.spaHandler(s.WebFS)

	t.Run("fremder Host wird nicht weitergereicht", func(t *testing.T) {
		for _, ziel := range []string{"//evil.com/", "//evil.com//", "/\\evil.com/"} {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ziel, nil))

			if loc := rec.Header().Get("Location"); loc != "" {
				t.Errorf("%q: Location=%q — es darf gar kein Redirect entstehen", ziel, loc)
			}
			if rec.Code != http.StatusNotFound {
				t.Errorf("%q: Status %d, erwartet 404", ziel, rec.Code)
			}
		}
	})

	// Der eigentliche Zweck des Redirects muss erhalten bleiben: /funktion/ und
	// /funktion sind sonst zwei Adressen mit demselben Inhalt.
	t.Run("lokaler Pfad wird weiter umgeleitet", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/funktion/", nil))

		if rec.Code != http.StatusMovedPermanently {
			t.Fatalf("Status %d, erwartet 301", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/funktion" {
			t.Errorf("Location=%q, erwartet /funktion", loc)
		}
	})
}

func TestIstLokalerPfad(t *testing.T) {
	lokal := []string{"/", "/funktion", "/en/preise", "/a/b/c", "/foo:bar"}
	fremd := []string{"//evil.com", "///evil.com", "/\\evil.com", "https://evil.com", "evil.com", ""}

	for _, p := range lokal {
		if !istLokalerPfad(p) {
			t.Errorf("%q gilt als fremd, ist aber ein gewöhnlicher Pfad", p)
		}
	}
	for _, p := range fremd {
		if istLokalerPfad(p) {
			t.Errorf("%q gilt als lokal — damit wäre der Redirect offen", p)
		}
	}
}
