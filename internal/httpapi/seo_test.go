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

// testDist baut ein dist/ nach, wie es web/prerender.mjs erzeugt: eine
// vorgerenderte Startseite, eine Unterseite, die englische Fassung, die
// 404-Seiten, die Hülle für die angemeldete Oberfläche und seo.json.
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

func hole(t *testing.T, s *Server, pfad string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.spaHandler(s.WebFS).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, pfad, nil))
	return rec
}

func TestSPAVorgerenderteSeiten(t *testing.T) {
	s := testServer()

	for pfad, erwartet := range map[string]string{
		"/":         "Startseite",
		"/funktion": "Funktion",
		"/en":       "Home",
	} {
		rec := hole(t, s, pfad)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: Status %d, erwartet 200", pfad, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), erwartet) {
			t.Fatalf("%s: liefert nicht die vorgerenderte Seite %q", pfad, erwartet)
		}
	}
}

// Der Kern der Änderung: Ein unbekannter Pfad ist eine 404 und nicht mehr die
// Startseite mit Status 200.
func TestSPAUnbekannterPfadIst404(t *testing.T) {
	s := testServer()

	rec := hole(t, s, "/gibt-es-nicht")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("Status %d, erwartet 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Nicht gefunden") {
		t.Fatalf("liefert nicht die deutsche 404-Seite: %s", rec.Body.String())
	}

	rec = hole(t, s, "/en/nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("Status %d, erwartet 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Not found") {
		t.Fatalf("liefert nicht die englische 404-Seite: %s", rec.Body.String())
	}
}

func TestSPAAppPfadeLiefernDieHuelle(t *testing.T) {
	s := testServer()

	rec := hole(t, s, "/agents/7f3e")
	if rec.Code != http.StatusOK {
		t.Fatalf("Status %d, erwartet 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Control Plane") {
		t.Fatalf("App-Pfad bekommt nicht die Hülle: %s", rec.Body.String())
	}
	if got := rec.Header().Get("X-Robots-Tag"); got != "noindex" {
		t.Fatalf("X-Robots-Tag = %q, erwartet noindex", got)
	}
}

func TestSPAStatischeDateienUndSchraegstrich(t *testing.T) {
	s := testServer()

	if rec := hole(t, s, "/assets/index-abc.js"); rec.Code != http.StatusOK {
		t.Fatalf("Asset: Status %d, erwartet 200", rec.Code)
	}

	rec := hole(t, s, "/funktion/")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("Status %d, erwartet 301", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/funktion" {
		t.Fatalf("Location = %q, erwartet /funktion", got)
	}
}

// Ohne vorgerendertes dist/ (alter Build) bleibt es beim alten Verhalten:
// alles fällt auf die index.html, niemand bekommt eine 404 zu sehen.
func TestSPAOhneVorrendernUnveraendert(t *testing.T) {
	s := &Server{WebFS: fstest.MapFS{
		"index.html": {Data: []byte("<html><body><div id=\"root\"></div></body></html>")},
	}}
	s.seo = ladeSEOIndex(s.WebFS)

	if rec := hole(t, s, "/agents/7f3e"); rec.Code != http.StatusOK {
		t.Fatalf("Status %d, erwartet 200", rec.Code)
	}
	if rec := hole(t, s, "/beliebig"); rec.Code != http.StatusOK {
		t.Fatalf("Status %d, erwartet 200", rec.Code)
	}
}

// Die Adresse der Installation steht erst zur Laufzeit fest — im
// vorgerenderten HTML steht ein Platzhalter, der nie beim Besucher ankommen darf.
func TestOriginPlatzhalterWirdErsetzt(t *testing.T) {
	s := testServer()
	body := hole(t, s, "/").Body.String()

	if strings.Contains(body, platzhalterOrigin) {
		t.Fatalf("Platzhalter steht noch im ausgelieferten HTML")
	}
	if !strings.Contains(body, `href="https://covey.example/"`) {
		t.Fatalf("Canonical trägt nicht die konfigurierte Adresse: %s", body)
	}

	// Ohne Konfiguration kommt die Adresse aus dem Request.
	ohne := testServerOhneSiteURL()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "covey.intern:8494"
	req.Header.Set("X-Forwarded-Proto", "https")
	ohne.spaHandler(ohne.WebFS).ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "https://covey.intern:8494/") {
		t.Fatalf("Adresse nicht aus dem Request abgeleitet: %s", rec.Body.String())
	}
}

// Alles, was eine Adresse dieser Instanz nach außen gibt, leitet sie aus dem
// Request ab: die Website, die Trigger-URL zum Kopieren, die Ziel-URL im Skill.
// Der Anlass ist ein Ausfall — solange all das an COVEY_PUBLIC_URL hing, konnte
// niemand die nach außen sichtbare Adresse richtig stellen, ohne die Adresse zu
// verstellen, über die die Sandboxen zurückverbinden. Am Ende stand eine
// Instanz mit korrekten Webhook-URLs und einer toten Data Plane.
//
// Dass PublicURL hier nicht mehr hineinreicht, hält inzwischen der Compiler
// fest: httpapi.Server kennt das Feld nicht mehr. Dieser Test deckt die andere
// Hälfte ab — dass die Ableitung aus dem Request auch stimmt.
func TestExterneAdressenKommenAusDemRequest(t *testing.T) {
	s := testServerOhneSiteURL()

	anfrage := func(pfad string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, pfad, nil)
		req.Host = "covey.example"
		req.Header.Set("X-Forwarded-Proto", "https")
		return req
	}

	// Website.
	rec := httptest.NewRecorder()
	s.spaHandler(s.WebFS).ServeHTTP(rec, anfrage("/"))
	if !strings.Contains(rec.Body.String(), `href="https://covey.example/"`) {
		t.Fatalf("Canonical nicht aus dem Request:\n%s", rec.Body.String())
	}

	// Trigger-URL — die kopiert jemand in ein Fremdsystem; eine interne
	// Adresse wäre dort wertlos.
	token := "geheim"
	view := s.webhookView(anfrage("/api/v1/agents/x/webhook"), agents.Agent{WebhookToken: &token})
	if got := view["url"]; got != "https://covey.example/api/trigger/geheim" {
		t.Fatalf("Trigger-URL = %v", got)
	}

	// Und mit gesetzter SiteURL gewinnt die Konfiguration.
	s.SiteURL = "https://covey.beispiel.de"
	view = s.webhookView(anfrage("/api/v1/agents/x/webhook"), agents.Agent{WebhookToken: &token})
	if got := view["url"]; got != "https://covey.beispiel.de/api/trigger/geheim" {
		t.Fatalf("SiteURL sticht den Request nicht: %v", got)
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
			t.Fatalf("robots.txt ohne %q:\n%s", zeile, robots)
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
			t.Fatalf("sitemap.xml ohne %q:\n%s", teil, sitemap)
		}
	}
	// Die Anmeldeseite ist nicht indexierbar und darf nicht auftauchen.
	if strings.Contains(sitemap, "/anmelden") {
		t.Fatalf("sitemap.xml enthält die Anmeldeseite:\n%s", sitemap)
	}
}

func testServerOhneSiteURL() *Server {
	s := &Server{WebFS: testDist()}
	s.seo = ladeSEOIndex(s.WebFS)
	return s
}
