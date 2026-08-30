package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
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
		{"/icon-192.png", "public, max-age=86400",
			"an icon keeps its name across releases"},
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
// the names of the previous one after a deploy. Only the shell is meant here —
// since #130 an unknown path is a bare 404 with no HTML to hold on to.
func TestCacheHeaderHTML(t *testing.T) {
	s := testServer()
	for _, pfad := range []string{"/", "/anmelden", "/agents/17"} {
		if cc := hole(t, s, pfad).Header().Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("%s: Cache-Control=%q, expected no-cache", pfad, cc)
		}
	}
}

// The build writes a .br and a .gz beside each asset (web/compress.mjs). The
// handler is supposed to hand them out — and only to a client that said it can
// read them.
func TestVorkomprimierteAssets(t *testing.T) {
	s := testServer()

	faelle := []struct {
		accept   string
		encoding string
		koerper  string
	}{
		{"br, gzip", "br", "brotli"},
		{"gzip", "gzip", "gzipped"},
		{"br;q=0, gzip", "gzip", "gzipped"},
		{"", "", "console.log(1)"},
		{"identity", "", "console.log(1)"},
	}
	for _, f := range faelle {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/assets/index-DOnPDnv_.js", nil)
		if f.accept != "" {
			req.Header.Set("Accept-Encoding", f.accept)
		}
		s.spaHandler(s.WebFS).ServeHTTP(rec, req)

		if got := rec.Header().Get("Content-Encoding"); got != f.encoding {
			t.Errorf("Accept-Encoding %q: Content-Encoding=%q, expected %q", f.accept, got, f.encoding)
		}
		if got := rec.Body.String(); got != f.koerper {
			t.Errorf("Accept-Encoding %q: body=%q, expected %q", f.accept, got, f.koerper)
		}
		// The type is the one of the original file — a browser that gets
		// octet-stream downloads the script instead of running it.
		if typ := rec.Header().Get("Content-Type"); !strings.Contains(typ, "javascript") {
			t.Errorf("Accept-Encoding %q: Content-Type=%q, expected javascript", f.accept, typ)
		}
		if vary := rec.Header().Get("Vary"); f.encoding != "" && vary != "Accept-Encoding" {
			t.Errorf("Accept-Encoding %q: Vary=%q, expected Accept-Encoding", f.accept, vary)
		}
	}
}

// A file for which the build produced no compressed form still has to be
// served — the fonts are already compressed inside, the images too.
func TestOhneVorkomprimierteForm(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/icon-192.png", nil)
	req.Header.Set("Accept-Encoding", "br, gzip")
	s.spaHandler(s.WebFS).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "png" {
		t.Errorf("status %d, body %q — expected 200 and the file itself", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding=%q, expected none", got)
	}
}

// mitKompression must not compress again what the handler already handed out
// encoded — a browser would get a gzip wrapper around a brotli body.
func TestKeineDoppelteKompression(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/index-DOnPDnv_.js", nil)
	req.Header.Set("Accept-Encoding", "br, gzip")
	mitKompression(s.spaHandler(s.WebFS)).ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "br" {
		t.Errorf("Content-Encoding=%q, expected br", got)
	}
	if got := rec.Body.String(); got != "brotli" {
		t.Errorf("body=%q, expected the precompressed file unchanged", got)
	}
}
