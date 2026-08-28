package httpapi

import (
	"net/http"
	"testing"
)

// Was a static file allowed to be cached — and for how long? Without the header
// a browser asks again on every visit; with a wrong one it keeps a file that
// has since changed.
func TestCacheHeaderStatischeDateien(t *testing.T) {
	s := testServer()

	faelle := []struct {
		pfad     string
		erwartet string
		warum    string
	}{
		{"/assets/index-DOnPDnv_.js", "public, max-age=31536000, immutable",
			"the name carries the content hash — a changed file gets a new address"},
		{"/assets/ohne-hash.js", "public, max-age=86400",
			"same directory, but the name says nothing about the content"},
		{"/landing/bild.jpg", "public, max-age=86400",
			"an image keeps its name across releases"},
	}
	for _, f := range faelle {
		rec := hole(t, s, f.pfad)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d, expected 200", f.pfad, rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != f.erwartet {
			t.Errorf("%s: Cache-Control=%q, expected %q (%s)", f.pfad, cc, f.erwartet, f.warum)
		}
	}
}

// The HTML names the assets of the current build. Whoever holds it hands out
// the names of the previous one after a deploy.
func TestCacheHeaderHTML(t *testing.T) {
	s := testServer()
	for _, pfad := range []string{"/", "/funktion", "/agents/17", "/gibtesnicht"} {
		if cc := hole(t, s, pfad).Header().Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("%s: Cache-Control=%q, expected no-cache", pfad, cc)
		}
	}
}
