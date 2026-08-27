package sandbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const katalog = `{
  "schema": 1,
  "generated_at": "2026-08-14T09:00:00Z",
  "workplaces": [
    {"name": "base", "label": "base", "description": "…",
     "images": [
       {"covey_version": "main",   "ref": "ghcr.io/x/covey-sandbox@sha256:aaa", "platforms": ["linux/amd64"]},
       {"covey_version": "v0.4.0", "ref": "ghcr.io/x/covey-sandbox@sha256:bbb"}
     ]},
    {"name": "dev", "label": "dev", "description": "…",
     "images": [{"covey_version": "main", "ref": "ghcr.io/x/covey-sandbox@sha256:ccc"}]}
  ]
}`

// Der Katalog beantwortet die Frage, die vorher jede Installation selbst
// beantworten musste: welches Image zu DIESER Fassung gehoert. Gepinnt auf den
// Digest, weil ein Tag verschiebbar ist und ein verschiebbarer Zeiger kein Pin.
func TestKatalogLiefertImagesDerLaufendenFassung(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(katalog))
	}))
	defer srv.Close()

	s := NewSource(srv.URL, nil, nil)
	images := s.Images(context.Background())
	// Der Testbinary sitzt auf keinem Release-Tag — also der rollende Eintrag.
	if got := images["base"]; got != "ghcr.io/x/covey-sandbox@sha256:aaa" {
		t.Fatalf("base = %q", got)
	}
	if got := images["dev"]; got != "ghcr.io/x/covey-sandbox@sha256:ccc" {
		t.Fatalf("dev = %q", got)
	}
}

// Kein Katalog konfiguriert: Dann gilt, was vorher galt. Ein Katalog ist eine
// Quelle mehr, keine Voraussetzung.
func TestOhneKatalogBleibtAllesWieVorher(t *testing.T) {
	var s *Source
	if s.Enabled() {
		t.Fatal("ein nil-Source darf nicht aktiv sein")
	}
	if got := s.Images(context.Background()); got != nil {
		t.Fatalf("ohne Katalog keine Bilder, bekam %v", got)
	}
}

// Die Reihenfolge ist die Aussage: Wer eine Umgebungsvariable gesetzt hat, hat
// das letzte Wort — ein Katalog, der sie ueberstimmen koennte, liesse eine
// fremde Datei entscheiden, was auf einem fremden Host laeuft.
func TestReihenfolgeUmgebungKatalogVoreinstellung(t *testing.T) {
	standard := Images(nil)
	katalog := map[string]string{"base": "ghcr.io/x@sha256:aaa", "dev": "ghcr.io/x@sha256:ccc"}
	env := map[string]string{"dev": "meins:1"}

	got := Resolve(env, katalog)
	if got["base"] != "ghcr.io/x@sha256:aaa" {
		t.Fatalf("der Katalog soll die Voreinstellung ersetzen: %q", got["base"])
	}
	if got["dev"] != "meins:1" {
		t.Fatalf("die Umgebung soll den Katalog schlagen: %q", got["dev"])
	}
	// Und ohne beides bleibt die kompilierte Voreinstellung stehen.
	if leer := Resolve(nil, nil); leer["base"] != standard["base"] {
		t.Fatalf("ohne Quellen die Voreinstellung: %q", leer["base"])
	}
}

// Woher der Katalog kommt, steht nicht in der Oberflaeche und nicht in einer
// Konstanten neben der Adresse des Quelltexts, sondern wird daraus abgeleitet:
// Ein Fork traegt damit seinen eigenen.
func TestVoreingestellteKatalogAdresse(t *testing.T) {
	want := "https://raw.githubusercontent.com/benjaminLedel/covey/catalog/sandbox-catalog.json"
	if got := DefaultCatalogURL(); got != want {
		t.Fatalf("DefaultCatalogURL() = %q", got)
	}
}

// Der Unterschied, an dem jeder Rat haengt: Ein Name ohne Registry existiert
// nur auf der Maschine, die ihn gebaut hat — dort heisst „nicht da" bauen. Eine
// veroeffentlichte Adresse holt `docker run` selbst.
func TestPullable(t *testing.T) {
	ziehbar := []string{
		"ghcr.io/benjaminledel/covey-sandbox@sha256:abc",
		"ghcr.io/benjaminledel/covey-sandbox:dev-latest",
		"registry.example.com:5000/team/sandbox:1",
		"localhost:5000/eigenes:1",
	}
	for _, ref := range ziehbar {
		if !Pullable(ref) {
			t.Errorf("Pullable(%q) = false", ref)
		}
	}
	lokal := []string{"covey-sandbox:latest", "covey-sandbox-dev:latest", "", "postgres:16"}
	for _, ref := range lokal {
		if Pullable(ref) {
			t.Errorf("Pullable(%q) = true — dann verschwindet der Bau-Hinweis", ref)
		}
	}
}

// Ein Release verschiebt den Schlüssel, unter dem gesucht wird: dieselbe
// Instanz fragte gestern nach `main` und heute nach `v0.8.0`. Ist die
// Katalog-Kopie älter als das Release, gibt es darauf keine Antwort — und der
// Rückfall auf den einkompilierten Namen (`covey-sandbox:latest`) ist auf einem
// Server der schlechteste verfügbare: ein Name, den es dort nachweislich nicht
// gibt, gewählt anstelle eines Bildes, das es nachweislich gibt. Auf einer
// Produktivinstanz stand damit eine Stunde lang die ganze Datenebene.
func TestOhneEintragFuerDieseFassungGiltDerRollendeEintrag(t *testing.T) {
	e := CatalogEntry{Name: "dev", Images: []CatalogImage{
		{CoveyVersion: RollingVersion, Ref: "ghcr.io/x/covey-sandbox@sha256:rollend"},
		{CoveyVersion: "v0.7.0", Ref: "ghcr.io/x/covey-sandbox@sha256:alt"},
	}}

	img, ok := e.ForBuild("v0.8.0")
	if !ok || img.Ref != "ghcr.io/x/covey-sandbox@sha256:rollend" {
		t.Errorf("eine unbekannte Fassung muss den rollenden Eintrag bekommen, bekam %q (%v)", img.Ref, ok)
	}
	// Die eigene Fassung schlägt den rollenden Eintrag weiterhin.
	if img, ok := e.ForBuild("v0.7.0"); !ok || img.Ref != "ghcr.io/x/covey-sandbox@sha256:alt" {
		t.Errorf("die eigene Fassung muss vorgehen, bekam %q", img.Ref)
	}
}

// Ein Katalog ohne rollenden Eintrag kann nicht helfen — dann bleibt es beim
// einkompilierten Namen, und das ist richtig so: eine Maschine ohne Katalog ist
// meistens die, die ihre Bilder selbst baut.
func TestOhneRollendenEintragBleibtEsBeiDerVoreinstellung(t *testing.T) {
	e := CatalogEntry{Name: "dev", Images: []CatalogImage{
		{CoveyVersion: "v0.7.0", Ref: "ghcr.io/x/covey-sandbox@sha256:alt"},
	}}
	if img, ok := e.ForBuild("v0.8.0"); ok {
		t.Errorf("ohne rollenden Eintrag darf nichts erfunden werden, bekam %q", img.Ref)
	}
}

// Und der Rückfall greift durch den ganzen Weg: was die Instanz auflöst, ist
// ein veröffentlichtes Bild und kein lokaler Bauname.
func TestDerRueckfallGehtDurchBisZurAufloesung(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(katalog))
	}))
	defer srv.Close()

	// Der Katalog kennt nur `main` und `v0.4.0`; diese Fassung steht auf
	// keinem der beiden.
	s := NewSource(srv.URL, nil, nil)
	images := s.Images(context.Background())
	aufgeloest := Resolve(nil, images)
	for _, name := range []string{"base", "dev"} {
		if !Pullable(aufgeloest[name]) {
			t.Errorf("%s löst auf %q auf — ein Name, den ein Server nicht ziehen kann", name, aufgeloest[name])
		}
	}
}
