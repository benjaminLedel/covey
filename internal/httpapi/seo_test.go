package httpapi

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"covey/internal/agents"
)

// testDist rebuilds a dist/ the way web/prerender.mjs produces it: a
// prerendered start page, a subpage, the English version, the 404 pages, the
// shell for the signed-in interface and seo.json.
func testDist() fs.FS {
	seo := seoIndex{
		URLs: []seoURL{
			{Path: "/", Lang: "de", Priority: 1, Alt: map[string]string{"de": "/", "en": "/en"}},
			{Path: "/en", Lang: "en", Priority: 1, Alt: map[string]string{"de": "/", "en": "/en"}},
			{Path: "/funktion", Lang: "de", Priority: 0.9,
				Alt: map[string]string{"de": "/funktion", "en": "/en/how-it-works"}},
		},
		AppPrefixes: []string{"/agents", "/secrets"},
	}
	rohSeo, _ := json.Marshal(seo)

	seite := func(titel string) *fstest.MapFile {
		return &fstest.MapFile{Data: []byte(
			`<html lang="de"><head><title>` + titel + `</title>` +
				`<link rel="canonical" href="` + platzhalterOrigin + `/"/></head>` +
				`<body><div id="root" data-prerendered="">Inhalt</div></body></html>`)}
	}

	return fstest.MapFS{
		"index.html":          seite("Startseite"),
		"funktion/index.html": seite("Funktion"),
		"en/index.html":       seite("Home"),
		"404.html":            seite("Nicht gefunden"),
		"en/404.html":         seite("Not found"),
		"app.html":            seite("Covey — Control Plane"),
		"assets/index-abc.js": {Data: []byte("console.log(1)")},
		"landing/bild.jpg":    {Data: []byte("jpeg")},
		"seo.json":            {Data: rohSeo},
	}
}

func testServer() *Server {
	s := &Server{WebFS: testDist(), SiteURL: "https://covey.example"}
	s.seo = ladeSEOIndex(s.WebFS)
	return s
}

func hole(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.spaHandler(s.WebFS).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestSPAVorgerenderteSeiten(t *testing.T) {
	s := testServer()

	for path, erwartet := range map[string]string{
		"/":         "Startseite",
		"/funktion": "Funktion",
		"/en":       "Home",
	} {
		rec := hole(t, s, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d, expected 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), erwartet) {
			t.Fatalf("%s: does not serve the prerendered page %q", path, erwartet)
		}
	}
}

// The heart of the change: an unknown path is a 404 and no longer the start
// page with status 200.
func TestSPAUnbekannterPfadIst404(t *testing.T) {
	s := testServer()

	rec := hole(t, s, "/gibt-es-nicht")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, expected 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Nicht gefunden") {
		t.Fatalf("does not serve the German 404 page: %s", rec.Body.String())
	}

	rec = hole(t, s, "/en/nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, expected 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Not found") {
		t.Fatalf("does not serve the English 404 page: %s", rec.Body.String())
	}
}

func TestSPAAppPfadeLiefernDieHuelle(t *testing.T) {
	s := testServer()

	rec := hole(t, s, "/agents/7f3e")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, expected 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Control Plane") {
		t.Fatalf("app path does not get the shell: %s", rec.Body.String())
	}
	if got := rec.Header().Get("X-Robots-Tag"); got != "noindex" {
		t.Fatalf("X-Robots-Tag = %q, expected noindex", got)
	}
}

func TestSPAStatischeDateienUndSchraegstrich(t *testing.T) {
	s := testServer()

	if rec := hole(t, s, "/assets/index-abc.js"); rec.Code != http.StatusOK {
		t.Fatalf("asset: status %d, expected 200", rec.Code)
	}

	rec := hole(t, s, "/funktion/")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status %d, expected 301", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/funktion" {
		t.Fatalf("Location = %q, expected /funktion", got)
	}
}

// Without a prerendered dist/ (old build) the old behaviour remains:
// everything falls back to index.html, nobody gets to see a 404.
func TestSPAOhneVorrendernUnveraendert(t *testing.T) {
	s := &Server{WebFS: fstest.MapFS{
		"index.html": {Data: []byte("<html><body><div id=\"root\"></div></body></html>")},
	}}
	s.seo = ladeSEOIndex(s.WebFS)

	if rec := hole(t, s, "/agents/7f3e"); rec.Code != http.StatusOK {
		t.Fatalf("status %d, expected 200", rec.Code)
	}
	if rec := hole(t, s, "/beliebig"); rec.Code != http.StatusOK {
		t.Fatalf("status %d, expected 200", rec.Code)
	}
}

// The installation's address is only settled at runtime — the prerendered HTML
// contains a placeholder that must never reach the visitor.
func TestOriginPlatzhalterWirdErsetzt(t *testing.T) {
	s := testServer()
	body := hole(t, s, "/").Body.String()

	if strings.Contains(body, platzhalterOrigin) {
		t.Fatalf("placeholder still present in the served HTML")
	}
	if !strings.Contains(body, `href="https://covey.example/"`) {
		t.Fatalf("canonical does not carry the configured address: %s", body)
	}

	// Without configuration the address comes from the request.
	ohne := testServerOhneSiteURL()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "covey.intern:8494"
	req.Header.Set("X-Forwarded-Proto", "https")
	ohne.spaHandler(ohne.WebFS).ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "https://covey.intern:8494/") {
		t.Fatalf("address not derived from the request: %s", rec.Body.String())
	}
}

// Everything that hands an address of this instance outwards derives it from
// the request: the website, the trigger URL to copy, the target URL in the
// skill. The occasion was an outage — as long as all of that hung on
// COVEY_PUBLIC_URL, nobody could correct the externally visible address without
// misadjusting the address over which the sandboxes connect back. The result was
// an instance with correct webhook URLs and a dead data plane.
//
// That PublicURL no longer reaches in here is by now held in place by the
// compiler: httpapi.Server does not know the field any more. This test covers
// the other half — that the derivation from the request is also correct.
func TestExterneAdressenKommenAusDemRequest(t *testing.T) {
	s := testServerOhneSiteURL()

	anfrage := func(path string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "covey.example"
		req.Header.Set("X-Forwarded-Proto", "https")
		return req
	}

	// Website.
	rec := httptest.NewRecorder()
	s.spaHandler(s.WebFS).ServeHTTP(rec, anfrage("/"))
	if !strings.Contains(rec.Body.String(), `href="https://covey.example/"`) {
		t.Fatalf("canonical not taken from the request:\n%s", rec.Body.String())
	}

	// Trigger URL — someone copies this into a foreign system; an internal
	// address would be worthless there.
	token := "geheim"
	view := s.webhookView(anfrage("/api/v1/agents/x/webhook"), agents.Agent{WebhookToken: &token})
	if got := view["url"]; got != "https://covey.example/api/trigger/geheim" {
		t.Fatalf("trigger URL = %v", got)
	}

	// And with SiteURL set the configuration wins.
	s.SiteURL = "https://covey.beispiel.de"
	view = s.webhookView(anfrage("/api/v1/agents/x/webhook"), agents.Agent{WebhookToken: &token})
	if got := view["url"]; got != "https://covey.beispiel.de/api/trigger/geheim" {
		t.Fatalf("SiteURL does not beat the request: %v", got)
	}
}

func TestRobotsUndSitemap(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.handleRobots(rec, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))
	robots := rec.Body.String()
	for _, zeile := range []string{
		"Disallow: /api/",
		"Disallow: /agents",
		"Disallow: /secrets",
		"Sitemap: https://covey.example/sitemap.xml",
	} {
		if !strings.Contains(robots, zeile) {
			t.Fatalf("robots.txt without %q:\n%s", zeile, robots)
		}
	}

	rec = httptest.NewRecorder()
	s.handleSitemap(rec, httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil))
	sitemap := rec.Body.String()
	for _, teil := range []string{
		"<loc>https://covey.example/funktion</loc>",
		`hreflang="en" href="https://covey.example/en/how-it-works"`,
		`hreflang="x-default" href="https://covey.example/funktion"`,
	} {
		if !strings.Contains(sitemap, teil) {
			t.Fatalf("sitemap.xml without %q:\n%s", teil, sitemap)
		}
	}
	// The sign-in page is not indexable and must not show up.
	if strings.Contains(sitemap, "/anmelden") {
		t.Fatalf("sitemap.xml contains the sign-in page:\n%s", sitemap)
	}
}

func testServerOhneSiteURL() *Server {
	s := &Server{WebFS: testDist()}
	s.seo = ladeSEOIndex(s.WebFS)
	return s
}
