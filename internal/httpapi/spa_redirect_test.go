package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// TestSPARedirectBleibtLokal: the trailing-slash redirect builds its target from
// the request path. If that does not stay local, it is an open redirect:
// `//evil.com/` would become `//evil.com`, and a browser reads that as a
// protocol-relative address on a foreign host.
//
// In production the ServeMux normalizes this away beforehand — but that is a
// property of the wiring, not of the handler. The test therefore attacks the
// handler DIRECTLY, otherwise it would check the Mux instead of the fix.
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

	// The actual purpose of the redirect must survive: otherwise /funktion/ and
	// /funktion are two addresses with the same content.
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
