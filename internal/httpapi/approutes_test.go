package httpapi

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"covey/internal/agents"
)

// testDist rebuilds a dist/ the way `vite build` produces it since #130: the
// shell, the hashed assets with their precompressed neighbours, and the route
// list the build writes from web/src/public/routes.ts.
func testDist() fs.FS {
	routen := appRouten{
		Prefixes: []string{"/agents", "/secrets"},
		Offen:    []string{"/anmelden", "/en/sign-in", "/registrieren", "/en/sign-up"},
	}
	rohRouten, _ := json.Marshal(routen)

	huelle := &fstest.MapFile{Data: []byte(
		`<html lang="en"><head><title>Covey</title></head>` +
			`<body><div id="root"></div></body></html>`)}

	return fstest.MapFS{
		"index.html":               huelle,
		"assets/index-DOnPDnv_.js": {Data: []byte("console.log(1)")},
		// What web/compress.mjs writes beside it. The content is not real
		// brotli — the handler passes it through, it does not read it.
		"assets/index-DOnPDnv_.js.br": {Data: []byte("brotli")},
		"assets/index-DOnPDnv_.js.gz": {Data: []byte("gzipped")},
		// Not everything under assets/ has to be hashed — the cache header
		// distinguishes, so both cases belong in the test FS.
		"assets/ohne-hash.js": {Data: []byte("console.log(2)")},
		// Icons and the manifest keep their names across releases — the cache
		// header has to tell them apart from the hashed assets.
		"icon-192.png":    {Data: []byte("png")},
		"app-routes.json": {Data: rohRouten},
	}
}

func testServer() *Server {
	s := &Server{WebFS: testDist(), SiteURL: "https://covey.example"}
	s.routen = ladeAppRouten(s.WebFS)
	return s
}

func hole(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.spaHandler(s.WebFS).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// The root and the two open addresses are the interface, not a 404: whoever
// enters here has to reach the sign-in.
func TestSPAOffeneAdressenLiefernDieHuelle(t *testing.T) {
	s := testServer()

	for _, pfad := range []string{"/", "/anmelden", "/en/sign-in", "/registrieren", "/en/sign-up"} {
		rec := hole(t, s, pfad)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d, expected 200", pfad, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `id="root"`) {
			t.Fatalf("%s does not get the shell: %s", pfad, rec.Body.String())
		}
	}
}

// A typo says so instead of quietly showing the sign-in. Since #130 that is the
// plain answer of the server — there is no prerendered 404 page left to serve.
func TestSPAUnbekannterPfadIst404(t *testing.T) {
	s := testServer()

	for _, pfad := range []string{"/gibt-es-nicht", "/funktion", "/en/how-it-works"} {
		if rec := hole(t, s, pfad); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status %d, expected 404", pfad, rec.Code)
		}
	}
}

func TestSPAAppPfadeLiefernDieHuelle(t *testing.T) {
	s := testServer()

	rec := hole(t, s, "/agents/7f3e")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, expected 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `id="root"`) {
		t.Fatalf("app path does not get the shell: %s", rec.Body.String())
	}
	if got := rec.Header().Get("X-Robots-Tag"); got != "noindex" {
		t.Fatalf("X-Robots-Tag = %q, expected noindex", got)
	}
}

func TestSPAStatischeDateienUndSchraegstrich(t *testing.T) {
	s := testServer()

	if rec := hole(t, s, "/assets/index-DOnPDnv_.js"); rec.Code != http.StatusOK {
		t.Fatalf("asset: status %d, expected 200", rec.Code)
	}

	rec := hole(t, s, "/agents/")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status %d, expected 301", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/agents" {
		t.Fatalf("Location = %q, expected /agents", got)
	}
}

// Without a route list (an old build, a hand-built FS) everything falls to the
// shell: serving the interface is the safer wrong answer than a 404 on a page
// that does exist.
func TestSPAOhneRoutenlisteFaelltAllesAufDieHuelle(t *testing.T) {
	s := &Server{WebFS: fstest.MapFS{
		"index.html": {Data: []byte(`<html><body><div id="root"></div></body></html>`)},
	}}
	s.routen = ladeAppRouten(s.WebFS)

	for _, pfad := range []string{"/agents/7f3e", "/beliebig"} {
		if rec := hole(t, s, pfad); rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d, expected 200", pfad, rec.Code)
		}
	}
}

// The installation's address is only settled at runtime — it goes into what
// somebody copies out of the interface into a foreign system.
func TestExterneAdressenKommenAusDemRequest(t *testing.T) {
	s := &Server{WebFS: testDist()}
	s.routen = ladeAppRouten(s.WebFS)

	anfrage := func(path string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "covey.example"
		req.Header.Set("X-Forwarded-Proto", "https")
		return req
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

// Nothing here belongs in an index — the pages worth finding live on the
// website, under their own domain (#129).
func TestRobotsSperrtAlles(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.handleRobots(rec, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))
	robots := rec.Body.String()

	if !strings.Contains(robots, "Disallow: /\n") {
		t.Fatalf("robots.txt does not lock everything:\n%s", robots)
	}
	// A sitemap of an application that is not indexed would point at nothing.
	if strings.Contains(robots, "Sitemap:") {
		t.Fatalf("robots.txt still names a sitemap:\n%s", robots)
	}
}

// The route list is only worth something if the build really writes it. This
// test therefore runs against the actual web/dist instead of a hand-built FS —
// it is the one place where the two halves of #130 have to agree: what
// vite.config.ts emits and what this package reads.
//
// Without a build there is no dist/; then there is nothing to check here. CI
// builds the frontend before the Go tests (build-web → test-go), so it does
// not skip.
func TestRoutenlisteAusDemEchtenBuild(t *testing.T) {
	dist := os.DirFS("../../web/dist")
	if _, err := fs.Stat(dist, "app-routes.json"); err != nil {
		t.Skip("no web/dist — run npm run build first")
	}

	s := &Server{WebFS: dist}
	s.routen = ladeAppRouten(dist)
	if !s.routen.bekannt() {
		t.Fatal("the build wrote app-routes.json without app prefixes")
	}

	faelle := []struct {
		pfad   string
		status int
	}{
		{"/", http.StatusOK},
		{"/anmelden", http.StatusOK},
		{"/en/sign-in", http.StatusOK},
		{"/registrieren", http.StatusOK},
		{"/agents", http.StatusOK},
		{"/agents/7f3e", http.StatusOK},
		// Was einmal die Website war, gibt es hier nicht mehr.
		{"/funktion", http.StatusNotFound},
		{"/en/how-it-works", http.StatusNotFound},
		{"/docs/was-ist-covey", http.StatusNotFound},
	}
	for _, f := range faelle {
		if rec := hole(t, s, f.pfad); rec.Code != f.status {
			t.Errorf("%s: status %d, expected %d", f.pfad, rec.Code, f.status)
		}
	}
}
